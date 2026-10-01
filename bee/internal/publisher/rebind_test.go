package publisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Exercise the real publication/retry loop: a healthy Hive connection must not
// keep advertising a binding to sockets replaced by a Herdr update or restart.
func TestPublisherRebindsChangedEndpoints(t *testing.T) {
	for _, scenario := range []struct {
		endpoint string
		missing  bool
	}{{"herdr.sock", false}, {"herdr-client.sock", false}, {"herdr.sock", true}, {"herdr-client.sock", true}} {
		t.Run(fmt.Sprintf("%s/missing=%t", scenario.endpoint, scenario.missing), func(t *testing.T) {
			endpoint := scenario.endpoint
			dir, err := os.MkdirTemp("", "bee-rebind-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			listeners := map[string]net.Listener{}
			listen := func(name string) {
				l, err := net.Listen("unix", filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				listeners[name] = l
			}
			listen("herdr.sock")
			listen("herdr-client.sock")
			defer func() {
				for _, l := range listeners {
					l.Close()
				}
			}()
			sessions, err := json.Marshal(map[string]any{"sessions": []herdr.Session{{Name: "default", Running: true, Socket: filepath.Join(dir, "herdr.sock")}}})
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
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			host, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			block, err := ssh.MarshalPrivateKey(key, "test")
			if err != nil {
				t.Fatal(err)
			}
			identity := filepath.Join(dir, "identity")
			if err := os.WriteFile(identity, pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal(err)
			}
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			known := filepath.Join(dir, "known_hosts")
			if err := os.WriteFile(known, []byte(knownhosts.Line([]string{l.Addr().String()}, host.PublicKey())+"\n"), 0600); err != nil {
				l.Close()
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			var workers sync.WaitGroup
			defer func() { cancel(); l.Close(); workers.Wait() }()
			published := make(chan *ssh.ServerConn, 4)
			var generation atomic.Uint64
			workers.Go(func() {
				for {
					raw, err := l.Accept()
					if err != nil {
						return
					}
					workers.Go(func() {
						defer raw.Close()
						raw.SetDeadline(time.Now().Add(5 * time.Second))
						server := &ssh.ServerConfig{NoClientAuth: true}
						server.AddHostKey(host)
						conn, channels, requests, err := ssh.NewServerConn(raw, server)
						if err != nil {
							return
						}
						raw.SetDeadline(time.Time{})
						defer conn.Close()
						stop := context.AfterFunc(ctx, func() { conn.Close() })
						defer stop()
						workers.Go(func() {
							for ch := range channels {
								ch.Reject(ssh.Prohibited, "unexpected channel")
							}
						})
						for r := range requests {
							if r.Type != "publish@herdr-hive/v1" {
								r.Reply(true, nil)
								continue
							}
							shares := []Share{{Key: "one", Label: "default", ID: "s-test", Name: "test", API: true, Generation: fmt.Sprintf("%032d", generation.Add(1))}}
							data, _ := json.Marshal(shares)
							r.Reply(true, data)
							select {
							case published <- conn:
							case <-ctx.Done():
								return
							}
						}
					})
				}
			})
			state := &State{}
			workers.Go(func() {
				Run(ctx, config.Config{Hive: l.Addr().String(), IdentityFile: identity, KnownHosts: known, Name: "test", Rules: []config.Rule{{Session: "default", Key: "one"}}}, state)
			})
			awaitPublication := func() *ssh.ServerConn {
				t.Helper()
				select {
				case conn := <-published:
					return conn
				case <-time.After(5 * time.Second):
					t.Fatalf("no fresh publication after endpoint replacement; status: %+v", state.Get())
					return nil
				}
			}
			check := func(conn *ssh.ServerConn) {
				t.Helper()
				payload, _ := json.Marshal(map[string]string{"Key": "one", "Kind": "check"})
				ch, requests, err := conn.OpenChannel("bee-stream-v1", payload)
				if err != nil {
					t.Fatalf("fresh publication cannot open the native session: %v", err)
				}
				go ssh.DiscardRequests(requests)
				ch.Close()
			}
			old := awaitPublication()
			check(old)
			// A healthy binding must survive the same monitoring interval.
			select {
			case <-published:
				t.Fatal("healthy endpoints caused an unnecessary republish")
			case <-time.After(1200 * time.Millisecond):
			}
			// Keep the old inode alive while creating the replacement, as live
			// handoff can do; this prevents inode reuse hiding the regression.
			oldPath := filepath.Join(dir, endpoint+".old")
			if err := os.Rename(filepath.Join(dir, endpoint), oldPath); err != nil {
				t.Fatal(err)
			}
			oldListener := listeners[endpoint]
			defer oldListener.Close()
			if scenario.missing {
				deadline := time.Now().Add(3 * time.Second)
				for state.Get().Connected && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				status := state.Get()
				if status.Connected || len(status.Shares) != 0 || status.Error == "" {
					t.Fatalf("missing endpoint still advertised as online: %+v", status)
				}
			}
			listen(endpoint)
			fresh := awaitPublication()
			if fresh == old {
				t.Fatal("endpoint replacement reused the stale SSH publication")
			}
			check(fresh)
			payload, _ := json.Marshal(map[string]string{"Key": "one", "Kind": "check"})
			if ch, _, err := old.OpenChannel("bee-stream-v1", payload); err == nil {
				ch.Close()
				t.Fatal("old publication remained usable after rebind")
			}
		})
	}
}
