package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
	"github.com/deepshape-ai/herdr-hive/internal/update"
)

func (a App) update(ctx context.Context) error {
	installer, e := update.Open("bee")
	if e != nil {
		return e
	}
	defer installer.Close()
	before, running := publisher.Control(a.Dir, "status")
	if running != nil {
		if !errors.Is(running, os.ErrNotExist) && !errors.Is(running, syscall.ECONNREFUSED) {
			return fmt.Errorf("cannot inspect running Bee: %w", running)
		}
		// A missing socket is not sufficient evidence: a daemon may be starting.
		if e = os.MkdirAll(a.Dir, 0700); e != nil {
			return e
		}
		lock, err := os.OpenFile(filepath.Join(a.Dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return fmt.Errorf("Bee is starting or busy; retry update once status is available")
		}
	}

	if running == nil && before.Executable != installer.Executable {
		return fmt.Errorf("running Bee belongs to another installation: %s", before.Executable)
	}
	fmt.Fprintln(os.Stderr, "Checking the latest Bee release (download deadline: 3 minutes)...")
	result, e := installer.Install(ctx)
	if e != nil {
		return e
	}
	// Retry activation even if a preceding update installed the file but lost its acknowledgement.
	if running == nil && before.Version != result.Version {
		if _, e = publisher.Control(a.Dir, "restart"); e != nil {
			return fmt.Errorf("Bee %s installed; publisher restart failed: %w", result.Version, e)
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			current, err := publisher.Control(a.Dir, "status")
			if err == nil && current.Version == result.Version && current.Executable == result.Executable {
				return a.emit(struct {
					update.Result
					Publisher publisher.Status `json:"publisher"`
				}{result, current})
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return fmt.Errorf("Bee %s installed; publisher restart not confirmed; run bee status", result.Version)
	}
	return a.emit(result)
}
