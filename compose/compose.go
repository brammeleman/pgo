package compose

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/miekg/pgo/osutil"
	"go.science.ru.nl/log"
)

type Compose struct {
	user  string      // what user to use
	dir   string      // where to put it
	ports []PortRange // ports from config
}

// New returns a pointer to an intialized Compose.
func New(user, directory string, ports []PortRange) *Compose {
	g := &Compose{
		user:  user,
		dir:   directory,
		ports: ports,
	}
	return g
}

func (c *Compose) run(args ...string) ([]byte, error) {
	ctx := context.TODO()
	cmd := exec.CommandContext(ctx, "podman-compose", args...)
	cmd.Dir = c.dir

	if os.Geteuid() == 0 {
		uid, gid := osutil.User(c.user)
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
		home := osutil.Home(c.user)
		path := "/usr/sbin:/usr/bin:/sbin:/bin"
		cmd.Env = []string{env("HOME", home), env("PATH", path)}
	}

	log.Debugf("running in %q as %q %v (env: %v)", cmd.Dir, c.user, cmd.Args, cmd.Env)

	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Debug(string(out))
	}

	return bytes.TrimSpace(out), err
}

func env(k, v string) string { return k + "=" + v }

func (c *Compose) Build() ([]byte, error) { return c.run("build") }
func (c *Compose) Down() ([]byte, error)  { return c.run("down") }
func (c *Compose) Up() ([]byte, error)    { return c.run("up", "-d") }
func (c *Compose) Pull() ([]byte, error)  { return c.run("pull") }
func (c *Compose) Logs() ([]byte, error)  { return c.run("logs") } // logs -f needs more work
func (c *Compose) Ps() ([]byte, error)    { return c.run("ps") }
