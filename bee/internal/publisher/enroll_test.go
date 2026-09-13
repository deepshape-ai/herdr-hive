package publisher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
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

func TestEnrollTransport(t *testing.T) {
	for _, mode := range []string{"success", "reject", "wrong-host", "cancel", "bad-response"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			_, private, _ := ed25519.GenerateKey(rand.Reader)
			identity, _ := ssh.NewSignerFromKey(private)
			block, _ := ssh.MarshalPrivateKey(private, "")
			path := filepath.Join(dir, "key")
			os.WriteFile(path, pem.EncodeToMemory(block), 0600)
			_, hp, _ := ed25519.GenerateKey(rand.Reader)
			host, _ := ssh.NewSignerFromKey(hp)
			l, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			known := filepath.Join(dir, "known")
			trust := host.PublicKey()
			if mode == "wrong-host" {
				trust = identity.PublicKey()
			}
			os.WriteFile(known, []byte(knownhosts.Line([]string{l.Addr().String()}, trust)+"\n"), 0600)
			token := "hreg-" + strings.Repeat("a", 64)
			done := make(chan struct{})
			go func() {
				defer close(done)
				raw, e := l.Accept()
				if e != nil {
					return
				}
				defer raw.Close()
				raw.SetDeadline(time.Now().Add(2 * time.Second))
				conf := &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
					if m.User() != "enroll" || string(k.Marshal()) != string(identity.PublicKey().Marshal()) {
						t.Error("unexpected identity")
					}
					return nil, nil
				}}
				conf.AddHostKey(host)
				c, chans, reqs, e := ssh.NewServerConn(raw, conf)
				if e != nil {
					return
				}
				defer c.Close()
				go func() {
					for ch := range chans {
						ch.Reject(ssh.Prohibited, "")
					}
				}()
				for r := range reqs {
					if mode == "cancel" {
						continue
					}
					var p struct {
						Version int
						Token   string
					}
					if r.Type != "enroll@herdr-hive/v1" || !r.WantReply || json.Unmarshal(r.Payload, &p) != nil || p.Version != 1 || p.Token != token {
						t.Error("invalid request")
					}
					b, _ := json.Marshal(map[string]any{"registered": true, "fingerprint": ssh.FingerprintSHA256(identity.PublicKey())})
					if mode == "bad-response" {
						b = []byte(`{"registered":false}`)
					}
					r.Reply(mode != "reject", b)
					return
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			fp, e := Enroll(ctx, config.Config{Hive: l.Addr().String(), IdentityFile: path, KnownHosts: known}, token)
			if mode == "success" {
				if e != nil || fp != ssh.FingerprintSHA256(identity.PublicKey()) {
					t.Fatal(fp, e)
				}
			} else if e == nil {
				t.Fatal("unexpected success")
			}
			<-done
		})
	}
	if _, e := Enroll(context.Background(), config.Config{}, "bad"); e == nil {
		t.Fatal("invalid token accepted")
	}
}
