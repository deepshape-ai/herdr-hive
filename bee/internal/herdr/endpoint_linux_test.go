package herdr

import (
	"errors"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBoundPinsBothEndpointInodesUntilClosed(t *testing.T) {
	for _, endpoint := range []string{"api", "client"} {
		t.Run(endpoint, func(t *testing.T) {
			b, listener := apiFixture(t)
			if len(b.lifetime.pins) != 2 {
				t.Fatalf("expected two endpoint pins, got %d", len(b.lifetime.pins))
			}
			for _, pin := range b.lifetime.pins {
				flags, err := unix.FcntlInt(pin.Fd(), unix.F_GETFD, 0)
				if err != nil || flags&unix.FD_CLOEXEC == 0 {
					t.Fatalf("pin not close-on-exec: flags=%d err=%v", flags, err)
				}
			}
			path, old, pin := b.Socket, b.apiInfo, b.lifetime.pins[0]
			if endpoint == "api" {
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				path, old, pin = b.clientPath, b.clientInfo, b.lifetime.pins[1]
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			replacement, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			now, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := pin.Stat()
			if err != nil || !os.SameFile(old, retained) {
				t.Fatalf("old endpoint inode was not retained: %v", err)
			}
			if os.SameFile(old, now) {
				t.Fatal("replacement reused a pinned inode")
			}
			if err := b.Check(); err == nil {
				t.Fatal("replacement endpoint was accepted")
			}
			copy := b
			copy.Close()
			b.Close()
			if err := b.Check(); err == nil {
				t.Fatal("closed binding was accepted")
			}
			for _, pin := range b.lifetime.pins {
				if _, err := pin.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("endpoint pin leaked after Close: %v", err)
				}
			}
		})
	}
}

func TestBindPartialFailureClosesFirstEndpointPin(t *testing.T) {
	b, _ := apiFixture(t)
	b.Close()
	if err := os.Remove(b.clientPath); err != nil {
		t.Fatal(err)
	}
	failed, err := Bind(b.Session)
	if err == nil {
		failed.Close()
		t.Fatal("binding succeeded without client endpoint")
	}
	if len(failed.lifetime.pins) != 1 {
		t.Fatal("test did not exercise partial endpoint pinning")
	}
	if _, err := failed.lifetime.pins[0].Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("first endpoint pin leaked after second endpoint failure: %v", err)
	}
	if err := failed.Check(); err == nil {
		t.Fatal("failed binding remained usable")
	}
	failed.Close()
}
