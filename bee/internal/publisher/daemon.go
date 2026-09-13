package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

// Control uses a private local socket; reload acknowledges after old streams close.
func Control(dir, op string) (Status, error) {
	c, e := net.DialTimeout("unix", filepath.Join(dir, "control.sock"), time.Second)
	if e != nil {
		return Status{}, e
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if e = json.NewEncoder(c).Encode(op); e != nil {
		return Status{}, e
	}
	var s Status
	e = json.NewDecoder(c).Decode(&s)
	return s, e
}
func Daemon(ctx context.Context, dir string) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("Bee is already running")
	}
	path := filepath.Join(dir, "control.sock")
	if len(path) > 100 {
		return errors.New("Bee configuration path is too long for a Unix socket")
	}
	os.Remove(path)
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer l.Close()
	defer os.Remove(path)
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	state := &State{}
	var mu sync.Mutex
	var cancel context.CancelFunc
	var done chan struct{}
	reload := func() {
		mu.Lock()
		defer mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
		}
		c, e := config.Load(dir)
		if e != nil {
			state.Set(Status{Error: e.Error()})
			return
		}
		state.Set(Status{Enabled: c.Enabled})
		if c.Enabled {
			var child context.Context
			child, cancel = context.WithCancel(ctx)
			done = make(chan struct{})
			go func() { defer close(done); Run(child, c, state) }()
		}
	}
	reload()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	}()
	go func() { <-ctx.Done(); l.Close() }()
	// Serialize control operations; callers cannot create an unbounded goroutine queue.
	for {
		conn, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		var op string
		e = json.NewDecoder(conn).Decode(&op)
		if e == nil {
			if op == "reload" {
				reload()
			}
			json.NewEncoder(conn).Encode(state.Get())
		}
		conn.Close()
		if e == nil && op == "reload" && !state.Get().Enabled {
			return nil
		}
	}
}
