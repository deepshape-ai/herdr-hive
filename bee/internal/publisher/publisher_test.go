package publisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"golang.org/x/crypto/ssh"
)

func TestHungSSHAgentIsCancelled(t *testing.T) {
	dir := t.TempDir()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	block, e := ssh.MarshalPrivateKeyWithPassphrase(key, "test", []byte("temporary"))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "identity")
	os.WriteFile(path, pem.EncodeToMemory(block), 0600)
	// Unix socket paths must stay short on macOS; the temporary directory is removed.
	short, e := os.MkdirTemp("", "bee-agent-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(short)
	agentPath := filepath.Join(short, "s")
	l, e := net.Listen("unix", agentPath)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	t.Setenv("SSH_AUTH_SOCK", agentPath)
	peer := make(chan net.Conn, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			peer <- c
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, cleanup, e := auth(ctx, config.Config{IdentityFile: path}); cleanup(); done <- e }()
	c := <-peer
	defer c.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("hung agent succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("SSH agent ignored cancellation")
	}
}
