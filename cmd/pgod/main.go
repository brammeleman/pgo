package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gliderlabs/ssh"
	"github.com/miekg/pgo/conf"
	flag "github.com/spf13/pflag"
	"go.science.ru.nl/log"
)

type ExecContext struct {
	ConfigSource string
	SAddr        string
	Debug        bool
	Restart      bool
	Root         bool
	Duration     time.Duration
}

func (exec *ExecContext) RegisterFlags(fs *flag.FlagSet) {
	if fs == nil {
		fs = flag.CommandLine
	}
	fs.SortFlags = false
	fs.StringVarP(&exec.ConfigSource, "config", "c", "", "config file to read")
	fs.StringVarP(&exec.SAddr, "ssh", "s", ":2222", "ssh address to listen on")
	fs.BoolVarP(&exec.Debug, "debug", "d", false, "enable debug logging")
	fs.BoolVarP(&exec.Restart, "restart", "r", false, "send SIGHUP when config changes")
	fs.BoolVarP(&exec.Root, "root", "o", true, "require root permission, setting to false can aid in debugging")
	fs.DurationVarP(&exec.Duration, "duration", "t", 5*time.Minute, "default duration between pulls")
}

var (
	ErrNotRoot  = errors.New("not root")
	ErrNoConfig = errors.New("-c flag is mandatory")
	ErrHUP      = errors.New("hangup requested")
)

func serveSSH(exec *ExecContext, controllerWG, workerWG *sync.WaitGroup, sshHandler ssh.Handler) error {
	l, err := net.Listen("tcp", exec.SAddr)
	if err != nil {
		return err
	}
	srv := &ssh.Server{Addr: exec.SAddr, Handler: sshHandler}
	srv.SetOption(ssh.PublicKeyAuth(func(ctx ssh.Context, _ ssh.PublicKey) bool { return true }))

	controllerWG.Add(1) // Ensure SSH server draining blocks application shutdown.
	go func() {
		defer controllerWG.Done()
		workerWG.Wait()              // Unblocks upon context cancellation and workers finishing.
		srv.Shutdown(context.TODO()) // TODO: Derive context tree more carefully from root.
	}()
	controllerWG.Add(1)
	go func() {
		defer controllerWG.Done()
		err := srv.Serve(l)
		switch {
		case err == nil:
		case errors.Is(err, ssh.ErrServerClosed):
		default:
			log.Fatal(err)
		}
	}()
	return nil
}

func run(exec *ExecContext) error {
	if os.Geteuid() != 0 && exec.Root {
		return ErrNotRoot
	}

	if exec.Debug {
		log.D.Set()
	}

	if exec.ConfigSource == "" {
		return ErrNoConfig
	}

	doc, err := os.ReadFile(exec.ConfigSource)
	if err != nil {
		return fmt.Errorf("reading config: %v", err)
	}
	c, err := conf.Parse(doc)
	if err != nil {
		return fmt.Errorf("parsing config: %v", err)
	}
	for _, s := range c.Services {
		if err := s.InitGitAndCompose(); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	var workerWG, controllerWG sync.WaitGroup
	defer controllerWG.Wait()

	// start a fake worker thread, that in the case of no actual threads, will call done on the workerWG (and more
	// importantly will now have seen at least one Add(1)). This will make sure the serveMetrics and serveSSH return
	// correctly on receiving ^C.
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		select {
		case <-ctx.Done():
			return
		}
	}()

	for _, s := range c.Services {
		log.Infof("Service %q with upstream %q", s.Name, s.Repository)
		workerWG.Add(1)
		go func(s1 *conf.Service) {
			defer workerWG.Done()
			s1.Track(ctx, exec.Duration)
		}(s)
	}

	sshHandler := newRouter(c)
	if err := serveSSH(exec, &controllerWG, &workerWG, sshHandler); err != nil {
		return err
	}
	log.Infof("Launched servers on port %s (ssh)", exec.SAddr)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if exec.Restart {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			conf.Track(ctx, exec.ConfigSource, done)
		}()
	}
	hup := make(chan struct{})
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		select {
		case s := <-done:
			cancel()
			if s == syscall.SIGHUP {
				close(hup)
			}
		case <-ctx.Done():
		}
	}()
	workerWG.Wait()
	select {
	case <-hup:
		return ErrHUP
	default:
	}
	return nil
}

func main() {
	exec := ExecContext{}
	exec.RegisterFlags(nil)
	flag.Parse()
	err := run(&exec)
	switch {
	case err == nil:
	case errors.Is(err, ErrHUP):
		// on HUP exit with exit status 2, so systemd can restart us (Restart=OnFailure)
		os.Exit(2)
	default:
		log.Fatal(err)
	}
}
