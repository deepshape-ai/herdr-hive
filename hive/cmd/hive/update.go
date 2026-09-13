package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/server"
	"github.com/deepshape-ai/herdr-hive/internal/update"
)

func snapshot(state string) (server.Snapshot, error) {
	var v server.Snapshot
	c, e := net.DialTimeout("unix", filepath.Join(state, "inspect.sock"), time.Second)
	if e != nil {
		return v, e
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	e = json.NewDecoder(io.LimitReader(c, 4<<20)).Decode(&v)
	return v, e
}
func upgrade(args []string) error {
	f := flag.NewFlagSet("update", flag.ContinueOnError)
	state := f.String("state-dir", "", "running Hive state directory; omit for an offline binary update")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("usage: hive update [--state-dir PATH]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	installer, e := update.Open("hive")
	if e != nil {
		return e
	}
	defer installer.Close()
	var before server.Snapshot
	if *state != "" {
		before, e = snapshot(*state)
		if e != nil {
			return fmt.Errorf("cannot inspect running Hive: %w", e)
		}
		if before.Executable != installer.Executable || before.PID < 2 {
			return fmt.Errorf("running Hive belongs to another installation: %s", before.Executable)
		}
	}
	fmt.Fprintln(os.Stderr, "Checking the latest Hive release (download deadline: 3 minutes)...")
	result, e := installer.Install(ctx)
	if e != nil {
		return e
	}
	activated := false
	if *state != "" {
		activated = before.Version == result.Version
		if !activated {
			if e = requestRestart(*state); e != nil {
				return fmt.Errorf("Hive %s installed; restart request failed: %w", result.Version, e)
			}
			var current server.Snapshot
			var err error
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				current, err = snapshot(*state)
				if err == nil && current.Version == result.Version && current.Executable == result.Executable {
					activated = true
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
			if !activated {
				return fmt.Errorf("Hive %s installed; restart not confirmed; inspect the service", result.Version)
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		update.Result
		Active bool `json:"active"`
	}{result, activated})
}

func requestRestart(state string) error {
	c, e := net.DialTimeout("unix", filepath.Join(state, "control.sock"), time.Second)
	if e != nil {
		return e
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if e = json.NewEncoder(c).Encode("restart"); e != nil {
		return e
	}
	var ok bool
	if e = json.NewDecoder(io.LimitReader(c, 128)).Decode(&ok); e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("restart rejected")
	}
	return nil
}
