package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func TestBoundCloseIsSharedAndRejectsFurtherUse(t *testing.T) {
	b, _ := apiFixture(t)
	if err := b.Check(); err != nil {
		t.Fatal(err)
	}
	copy := b
	copy.Close()
	b.Close()
	if err := b.Check(); err == nil {
		t.Fatal("closed binding accepted")
	}
	if conn, err := b.Dial(); err == nil {
		conn.Close()
		t.Fatal("closed binding connected")
	}
}

func TestBoundRejectsChangedEndpointModTime(t *testing.T) {
	b, _ := apiFixture(t)
	changed := b.apiInfo.ModTime().Add(time.Second)
	if err := os.Chtimes(b.Socket, changed, changed); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(); err == nil {
		t.Fatal("changed endpoint modification time accepted")
	}
}

func TestBoundCloseStopsActiveAndIdleSizingImmediately(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "idle"}[idle], func(t *testing.T) {
			b, _ := apiFixture(t)
			done := make(chan struct{})
			cancels := 0
			lock := &sizeLock{done: done, cancel: func() {
				cancels++
				close(done)
			}}
			b.sizing.locks["w1:p1"] = lock
			v := &sizeView{sizing: b.sizing, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
			if err := v.reconcile(); err != nil {
				t.Fatal(err)
			}
			if idle {
				v.close()
			}
			copy := b
			copy.Close()
			b.Close()
			select {
			case <-done:
			default:
				t.Fatal("publication close did not synchronously stop its helper")
			}
			if cancels != 1 {
				t.Fatal("copied bindings stopped the helper more than once", cancels)
			}
			if len(b.sizing.locks) != 0 {
				t.Fatal("closed publication retained its sizing registry")
			}
			v.close()
			late := &sizeView{sizing: b.sizing, active: true, held: map[string]bool{}, snapshot: v.snapshot}
			if err := late.reconcile(); err == nil {
				t.Fatal("closed publication accepted a new sizing reference")
			}
		})
	}
}

func TestBoundConcurrentCopiesWaitForSingleSizingClose(t *testing.T) {
	b, _ := apiFixture(t)
	done, cancelling, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	t.Cleanup(release)
	lock := &sizeLock{done: done, cancel: func() {
		close(cancelling)
		<-finish
		close(done)
	}}
	b.sizing.locks["w1:p1"] = lock
	first, second := make(chan struct{}), make(chan struct{})
	go func() { b.Close(); close(first) }()
	select {
	case <-cancelling:
	case <-time.After(time.Second):
		t.Fatal("publication close did not start helper shutdown")
	}
	copy := b
	var started sync.WaitGroup
	started.Add(1)
	go func() { started.Done(); copy.Close(); close(second) }()
	started.Wait()
	select {
	case <-second:
		t.Fatal("copied close returned before helper shutdown completed")
	default:
	}
	release()
	for _, closed := range []chan struct{}{first, second} {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("copied close deadlocked during helper shutdown")
		}
	}
}

func TestBoundCloseCancelsInFlightSizingAcquisitionWithoutLockInversion(t *testing.T) {
	b, listener := apiFixture(t)
	accepted, disconnected := make(chan struct{}), make(chan struct{})
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var request map[string]any
		if json.NewDecoder(c).Decode(&request) != nil {
			return
		}
		close(accepted)
		// Deliberately withhold geometry. Publication cancellation must close
		// this setup connection rather than wait for the three-second timeout.
		io.Copy(io.Discard, c)
		close(disconnected)
	}()
	v := &sizeView{sizing: b.sizing, ctx: context.Background(), active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	acquired := make(chan error, 1)
	go func() { acquired <- v.reconcile() }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("sizing acquisition did not query native geometry")
	}
	closed := make(chan struct{})
	go func() { b.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("publication close waited on setup or inverted lifetime/sizing locks")
	}
	select {
	case err := <-acquired:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("publication cancellation was misclassified", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled sizing acquisition did not return")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("cancelled setup connection leaked")
	}
	v.close()
}
