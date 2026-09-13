// Package app provides one command surface used by both the CLI and terminal UI.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

type App struct {
	Version    string
	Executable string
	Dir        string
	Out        io.Writer
}

func (a App) emit(v any) error { return json.NewEncoder(a.Out).Encode(v) }
func (a App) change(fn func(*config.Config) error) error {
	c, e := config.Update(a.Dir, fn)
	if e != nil {
		return e
	}
	if _, e = publisher.Control(a.Dir, "status"); e == nil {
		if _, e = publisher.Control(a.Dir, "reload"); e != nil {
			return e
		}
	}
	return a.emit(c)
}
func (a App) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.TUI(ctx)
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(a.Out, `bee configure --hive HOST:PORT --identity PATH [--known-hosts PATH]
bee name [NAME]
bee sessions
bee share SESSION
bee unshare SESSION
bee enable | disable | status | tui | open
bee update
bee run | restore
All noninteractive command results are JSON. Sharing grants full access to the selected named session to registered Hive members.`)
		return nil
	case "update":
		if len(args) != 1 {
			return errors.New("usage: bee update")
		}
		return a.update(ctx)
	case "open":
		if len(args) != 1 {
			return errors.New("usage: bee open")
		}
		action, err := herdr.OpenSettings(ctx, a.Dir)
		if err != nil {
			return err
		}
		return a.emit(map[string]string{"panel": action})
	case "tui":
		return a.TUI(ctx)
	case "run":
		return publisher.Daemon(ctx, a.Dir, a.Version, a.Executable)
	case "restore":
		c, e := config.Load(a.Dir)
		if e != nil {
			return e
		}
		if c.Enabled {
			return a.start(false)
		}
		return nil
	case "configure":
		f := flag.NewFlagSet("configure", flag.ContinueOnError)
		f.SetOutput(a.Out)
		h := f.String("hive", "", "Hive SSH host:port")
		id := f.String("identity", "", "SSH identity file")
		kh := f.String("known-hosts", "", "known_hosts override (default: existing setting or ~/.ssh/known_hosts)")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 || *h == "" || *id == "" {
			return errors.New("configure requires --hive and --identity")
		}
		if _, _, e := net.SplitHostPort(*h); e != nil {
			return e
		}
		ip, e := filepath.Abs(*id)
		if e != nil {
			return e
		}
		if _, e = os.Stat(ip); e != nil {
			return e
		}
		return a.change(func(c *config.Config) error {
			kp, err := knownHostsPath(*kh, c.KnownHosts)
			if err != nil {
				return err
			}
			if _, err = os.Stat(kp); err != nil {
				return err
			}
			c.Hive = *h
			c.IdentityFile = ip
			c.KnownHosts = kp
			return nil
		})
	case "name":
		if len(args) == 1 {
			c, e := config.Load(a.Dir)
			if e != nil {
				return e
			}
			return a.emit(map[string]string{"name": c.Name})
		}
		if len(args) != 2 {
			return errors.New("usage: bee name NAME")
		}
		return a.change(func(c *config.Config) error { c.Name = args[1]; return nil })
	case "sessions":
		if len(args) != 1 {
			return errors.New("usage: bee sessions")
		}
		s, e := herdr.Sessions()
		if e != nil {
			return e
		}
		return a.emit(s)
	case "share", "unshare":
		if len(args) != 2 {
			return errors.New("usage: bee share|unshare SESSION")
		}
		if args[0] == "share" {
			sessions, e := herdr.Sessions()
			if e != nil {
				return e
			}
			found := false
			for _, s := range sessions {
				if s.Name == args[1] && s.Running {
					if _, e = herdr.Bind(s); e != nil {
						return e
					}
					found = true
				}
			}
			if !found {
				return errors.New("select an existing running named session")
			}
		}
		return a.change(func(c *config.Config) error {
			for i, r := range c.Rules {
				if r.Session == args[1] {
					if args[0] == "unshare" {
						c.Rules = append(c.Rules[:i], c.Rules[i+1:]...)
					}
					return nil
				}
			}
			if args[0] == "share" {
				r, e := config.NewRule(args[1])
				if e != nil {
					return e
				}
				c.Rules = append(c.Rules, r)
			}
			return nil
		})
	case "enable":
		if len(args) != 1 {
			return errors.New("usage: bee enable")
		}
		_, e := config.Update(a.Dir, func(c *config.Config) error {
			if c.Hive == "" || c.IdentityFile == "" || c.KnownHosts == "" || len(c.Rules) == 0 {
				return errors.New("configure Hive and select at least one session first")
			}
			c.Enabled = true
			return nil
		})
		if e != nil {
			return e
		}
		return a.start(true)
	case "disable":
		if len(args) != 1 {
			return errors.New("usage: bee disable")
		}
		return a.change(func(c *config.Config) error { c.Enabled = false; return nil })
	case "status":
		if len(args) != 1 {
			return errors.New("usage: bee status")
		}
		s, e := publisher.Control(a.Dir, "status")
		if e != nil {
			c, e := config.Load(a.Dir)
			if e != nil {
				return e
			}
			s = publisher.Status{Enabled: c.Enabled}
			if c.Enabled {
				s.Error = "Bee is not running; run bee enable"
			}
		}
		return a.emit(s)
	default:
		return fmt.Errorf("unknown command %q; run bee help", args[0])
	}
}
func (a App) start(reload bool) error {
	if _, e := publisher.Control(a.Dir, "status"); e == nil && reload {
		if _, e = publisher.Control(a.Dir, "reload"); e != nil {
			return e
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	lastSpawn := time.Time{}
	attempts := 0
	var s publisher.Status
	var e error
	for time.Now().Before(deadline) {
		s, e = publisher.Control(a.Dir, "status")
		if e == nil && (s.Connected || s.Error != "") {
			a.emit(s)
			if s.Error != "" {
				return errors.New("publication pending; Bee will retry; inspect bee status")
			}
			return nil
		}
		if e != nil && attempts < 3 && time.Since(lastSpawn) >= time.Second {
			launched, err := a.spawnIfUnowned()
			if err != nil {
				return err
			}
			if launched {
				attempts++
				lastSpawn = time.Now()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e != nil {
		return fmt.Errorf("Bee did not start: %w", e)
	}
	a.emit(s)
	return errors.New("publication pending; inspect bee status")
}
func (a App) spawnIfUnowned() (bool, error) {
	lock, e := os.OpenFile(filepath.Join(a.Dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return false, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return false, e
	}
	lock.Close()
	executable, e := a.executable()
	if e != nil {
		return false, e
	}
	cmd := exec.Command(executable, "run")
	cmd.Env = append(os.Environ(), "BEE_CONFIG_DIR="+a.Dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return false, e
	}
	cmd.Process.Release()
	return true, nil
}

func (a App) executable() (string, error) {
	if a.Executable != "" {
		return a.Executable, nil
	}
	return os.Executable()
}

// Omitted overrides preserve an existing trust store, including CLI-managed paths.
func knownHostsPath(requested, current string) (string, error) {
	if requested != "" {
		return filepath.Abs(requested)
	}
	if current != "" {
		return current, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}
