package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/registry"
	"golang.org/x/crypto/ssh"
)

// Each Herdr window and machine-add handshake needs its own aggregate slot.
// Two existing windows must not prevent a third member from joining Hive.
func TestAggregateViewerAdmissionAndRelease(t *testing.T) {
	dir := t.TempDir()
	host, identity := key(t), key(t)
	keys := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(keys, ssh.MarshalAuthorizedKey(identity.PublicKey()), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Open(filepath.Join(dir, "names.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(r, keys)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l, host) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	}()
	c, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{
		User: "hive", Auth: []ssh.AuthMethod{ssh.PublicKeys(identity)},
		HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	probe, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	probe.Stdin = strings.NewReader("printf '\n%s\n' 'herdr-remote-output-ready:1'\nuname -s\nuname -m\n")
	output, err := probe.Output("/bin/sh -s")
	probe.Close()
	if err != nil || string(output) != "\nherdr-remote-output-ready:1\nLinux\nx86_64\n" {
		t.Fatalf("framed platform probe = %q, %v", output, err)
	}
	open := func() (*ssh.Session, io.WriteCloser) {
		t.Helper()
		v, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { v.Close() })
		stdin, err := v.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Start("exec /herdr remote-client-bridge"); err != nil {
			t.Fatal(err)
		}
		return v, stdin
	}
	var viewers []*ssh.Session
	for i := 1; i <= 8; i++ {
		v, _ := open()
		viewers = append(viewers, v)
		eventually(t, func() bool { return s.Snapshot().GatewayViewers == i })
	}
	if got := s.Snapshot().MaxGatewayViewers; got != 8 {
		t.Fatalf("advertised limit = %d, want 8", got)
	}
	overflow, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer overflow.Close()
	var stderr bytes.Buffer
	overflow.Stderr = &stderr
	if err := overflow.Run("exec /herdr remote-client-bridge"); err == nil || !strings.Contains(stderr.String(), "gateway viewer capacity reached") {
		t.Fatalf("overflow was not rejected: %v, %q", err, stderr.String())
	}
	viewers[0].Close()
	eventually(t, func() bool { return s.Snapshot().GatewayViewers == 7 })
	replacement, stdin := open()
	eventually(t, func() bool { return s.Snapshot().GatewayViewers == 8 })
	stdin.Close()
	replacement.Close()
	for _, v := range viewers {
		v.Close()
	}
	eventually(t, func() bool { return s.Snapshot().GatewayViewers == 0 })
}
