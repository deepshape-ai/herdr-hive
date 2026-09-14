package publisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestDirectoryTransport(t *testing.T) {
	for _, mode := range []string{"success", "empty", "wrong-host", "cancel", "invalid", "incomplete", "oversize", "reject"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			_, private, _ := ed25519.GenerateKey(rand.Reader)
			identity, _ := ssh.NewSignerFromKey(private)
			block, _ := ssh.MarshalPrivateKey(private, "")
			keyPath := filepath.Join(dir, "identity")
			if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal(err)
			}
			_, hp, _ := ed25519.GenerateKey(rand.Reader)
			host, _ := ssh.NewSignerFromKey(hp)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			trust := host.PublicKey()
			if mode == "wrong-host" {
				trust = identity.PublicKey()
			}
			known := filepath.Join(dir, "known_hosts")
			if err := os.WriteFile(known, []byte(knownhosts.Line([]string{listener.Addr().String()}, trust)+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(3 * time.Second))
				conf := &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
					if m.User() != "hive" || string(k.Marshal()) != string(identity.PublicKey().Marshal()) {
						t.Error("unexpected reader identity")
					}
					return nil, nil
				}}
				conf.AddHostKey(host)
				conn, chans, reqs, err := ssh.NewServerConn(raw, conf)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						t.Error("unexpected channel")
						return
					}
					c, rs, err := ch.Accept()
					if err != nil {
						return
					}
					defer c.Close()
					for r := range rs {
						var command struct{ Command string }
						if r.Type != "exec" || ssh.Unmarshal(r.Payload, &command) != nil || command.Command != "list --json" {
							t.Error("unexpected command")
							return
						}
						if mode == "cancel" {
							continue
						}
						if mode == "reject" {
							r.Reply(false, nil)
							return
						}
						r.Reply(true, nil)
						out := `[{"id":"s-1","key":"one","name":"Build server","label":"default"},{"id":"s-2","key":"two","name":"Build server","label":"release"}]`
						switch mode {
						case "empty":
							out = `[]`
						case "invalid":
							out = `nope`
						case "incomplete":
							out = `[{"name":"missing-session"}]`
						case "oversize":
							out += strings.Repeat(" ", (1<<20)+1)
						}
						io.WriteString(c, out)
						c.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			shares, err := Directory(ctx, config.Config{Hive: listener.Addr().String(), IdentityFile: keyPath, KnownHosts: known})
			if mode == "success" {
				if err != nil || len(shares) != 2 || shares[0].Name != "Build server" {
					t.Fatalf("%+v %v", shares, err)
				}
			} else if mode == "empty" {
				if err != nil || len(shares) != 0 {
					t.Fatal(shares, err)
				}
			} else if err == nil {
				t.Fatal("invalid response succeeded")
			}
			<-done
		})
	}
}

func TestDirectoryBufferLimit(t *testing.T) {
	var out directoryBuffer
	// Match ssh.Session's io.Copy path without a source WriterTo shortcut.
	source := struct{ io.Reader }{strings.NewReader(strings.Repeat(" ", (1<<20)+1))}
	if _, err := io.Copy(&out, source); err == nil {
		t.Fatal("io.Copy bypassed the directory response limit")
	}
}
