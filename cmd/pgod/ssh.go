package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gliderlabs/ssh"
	"github.com/miekg/pgo/conf"
	"github.com/miekg/pgo/osutil"
	"go.science.ru.nl/log"
)

// pgoctl machine dhz//status
func newRouter(c *conf.Config) ssh.Handler {
	return func(ses ssh.Session) {
		pub := ses.PublicKey()
		if pub == nil {
			warnSession(ses, fmt.Sprintf("Connection denied for user %q because no public key supplied", ses.User()), http.StatusUnauthorized)
			return
		}
		if len(ses.Command()) == 0 {
			warnSession(ses, fmt.Sprintf("No commands in connection for user %q", ses.User()), http.StatusBadRequest)
			return
		}
		name, command, args, err := parseCommand(ses.Command())
		if err != nil {
			warnSession(ses, fmt.Sprintf("No correct commands in connection for user %q", ses.User()), http.StatusBadRequest)
			return
		}
		var s *conf.Service
		for i := range c.Services {
			if c.Services[i].Name == name {
				s = c.Services[i]
				break
			}
		}

		if s == nil {
			warnSession(ses, fmt.Sprintf("No service found with name %q", name), http.StatusNotFound)
			return
		}
		// Get the keys and chose *those*
		pubkeys, err := s.PublicKeys()
		if err != nil || len(pubkeys) == 0 {
			warnSession(ses, fmt.Sprintf("No public keys found for %q", name), http.StatusNotFound)
			return
		}

		key := -1
		for i := range pubkeys {
			if ssh.KeysEqual(pubkeys[i], ses.PublicKey()) {
				key = i
				break
			}
		}
		if key == -1 {
			warnSession(ses, fmt.Sprintf("Key for user %q does not match any for name %s", ses.User(), s.Name), http.StatusUnauthorized)
			return
		}

		route, ok := routes[command]
		if !ok {
			warnSession(ses, fmt.Sprintf("Command %q does not match any route", command), http.StatusNotAcceptable)
			return

		}
		log.Infof("Routing to %q for user %q", command, ses.User())
		route(s, ses, args)
		return
	}
}

var routes = map[string]func(*conf.Service, ssh.Session, []string){
	"up":   ComposeUp,
	"down": ComposeDown,
	"ps":   ComposePs,
	"pull": ComposePull,
	"logs": ComposeLogs,
	"ping": Ping,
}

func ComposeUp(s *conf.Service, ses ssh.Session, _ []string) {
	out, err := s.Compose.Up()
	exitSession(ses, out, err)
}

func ComposeDown(s *conf.Service, ses ssh.Session, _ []string) {
	out, err := s.Compose.Down()
	exitSession(ses, out, err)
}

func ComposePs(s *conf.Service, ses ssh.Session, _ []string) {
	out, err := s.Compose.Ps()
	exitSession(ses, out, err)
}

func ComposePull(s *conf.Service, ses ssh.Session, _ []string) {
	out, err := s.Compose.Pull()
	exitSession(ses, out, err)
}

func ComposeLogs(s *conf.Service, ses ssh.Session, _ []string) {
	out, err := s.Compose.Logs()
	exitSession(ses, out, err)
}

func Ping(s *conf.Service, ses ssh.Session, _ []string) {
	out := []byte("pong! - " + osutil.Hostname())
	exitSession(ses, out, nil)
}

// parseCommand parses: dhz//ps in name (dhz) and command (status) and optional args after it, split on space.
func parseCommand(s []string) (name, command string, args []string, error error) {
	items := strings.Split(s[0], "//")
	if len(items) != 2 {
		return "", "", nil, fmt.Errorf("expected name//command, got %s", s[0])
	}
	name = items[0]
	command = items[1]
	return name, command, s[1:], nil
}

func exitSession(ses ssh.Session, data []byte, err error) {
	if err != nil {
		log.Warning(err)
		io.WriteString(ses, http.StatusText(http.StatusInternalServerError))
		ses.Exit(http.StatusInternalServerError)
		return
	}
	ses.Write(data)
	ses.Exit(0)
}

func warnSession(ses ssh.Session, warn string, status int) {
	log.Warning(warn)
	io.WriteString(ses, http.StatusText(status)+": "+warn+"\n")
	ses.Exit(status)
}
