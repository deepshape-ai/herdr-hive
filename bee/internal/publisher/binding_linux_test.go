package publisher

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
)

func TestConnectEarlyFailuresReleaseEndpointPins(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "herdr.sock")
	clientSocket := filepath.Join(dir, "herdr-client.sock")
	for _, path := range []string{socket, clientSocket} {
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	sessions, err := json.Marshal(map[string]any{"sessions": []herdr.Session{{Name: "shared", Running: true, Socket: socket}}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(fixture, sessions, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "herdr")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat \"$BEE_TEST_SESSIONS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEE_HERDR_BINARY", binary)
	t.Setenv("BEE_TEST_SESSIONS", fixture)
	for _, mode := range []string{"later-session-missing", "ssh-setup-fails"} {
		t.Run(mode, func(t *testing.T) {
			c := config.Config{IdentityFile: filepath.Join(dir, "missing-key"), Rules: []config.Rule{{Session: "shared", Key: "one"}}}
			wantError := c.IdentityFile
			if mode == "later-session-missing" {
				c.Rules = append(c.Rules, config.Rule{Session: "missing", Key: "two"})
				wantError = "selected session"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := connect(ctx, c, &State{}); err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("expected early %s failure, got %v", mode, err)
			}
			fds, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			for _, fd := range fds {
				path, _ := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
				if path == socket || path == clientSocket {
					t.Fatalf("endpoint pin leaked after %s: fd=%s", mode, fd.Name())
				}
			}
		})
	}
}
