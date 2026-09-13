package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/enrollment"
	"github.com/deepshape-ai/herdr-hive/hive/internal/registry"
	"golang.org/x/crypto/ssh"
)

func TestEnrollmentGateway(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	os.WriteFile(keys, nil, 0600)
	token, secret, _ := enrollment.Generate(0, 1, "")
	tokens := filepath.Join(dir, "tokens")
	enrollment.SaveTokens(tokens, []enrollment.Token{token})
	r, _ := registry.Open(filepath.Join(dir, "names.json"))
	s := New(r, keys)
	s.Enrollment(tokens, filepath.Join(dir, "uses"), filepath.Join(dir, "registered"))
	host := key(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener, host) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	dial := func(user string, k ssh.Signer) (*ssh.Client, error) {
		return ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(k)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second})
	}
	enroll := func(k ssh.Signer, secret string, want bool) {
		t.Helper()
		c, e := dial("enroll", k)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		b, _ := json.Marshal(map[string]any{"version": 1, "token": secret})
		ok, response, e := c.SendRequest("enroll@herdr-hive/v1", true, b)
		if e != nil || ok != want {
			t.Fatalf("enroll ok=%t want=%t: %v", ok, want, e)
		}
		if ok {
			var v struct {
				Registered  bool
				Fingerprint string
			}
			if json.Unmarshal(response, &v) != nil || !v.Registered || v.Fingerprint != ssh.FingerprintSHA256(k.PublicKey()) {
				t.Fatal("bad response")
			}
		}
		if ok, _, _ := c.SendRequest("enroll@herdr-hive/v1", true, b); ok {
			t.Fatal("second request accepted")
		}
	}
	first, second := key(t), key(t)
	if c, e := dial("hive", first); e == nil {
		c.Close()
		t.Fatal("unregistered key")
	}
	c, e := dial("enroll", first)
	if e != nil {
		t.Fatal(e)
	}
	if ch, _, e := c.OpenChannel("session", nil); e == nil {
		ch.Close()
		t.Fatal("enroll opened session")
	}
	ok, _, e := c.SendRequest("publish@herdr-hive/v1", true, []byte(`{"version":1}`))
	if e != nil || ok {
		t.Fatal("enroll publication", e)
	}
	c.Close()
	enroll(first, secret, true)
	enroll(first, secret, true)
	enroll(second, secret, false)
	u, e := enrollment.LoadUses(s.EnrollmentState)
	if e != nil || u[token.ID].Uses != 1 {
		t.Fatal(u, e)
	}
	c, e = dial("hive", first)
	if e != nil {
		t.Fatal(e)
	}
	session, e := c.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if out, e := session.Output("list --json"); e != nil || string(out) != "[]\n" {
		t.Fatal(string(out), e)
	}
	c.Close()
	// Expiry and revocation apply even to an already registered key.
	expired := time.Now().Add(-time.Second)
	token.Expires = &expired
	enrollment.SaveTokens(tokens, []enrollment.Token{token})
	enroll(first, secret, false)
	enrollment.SaveTokens(tokens, []enrollment.Token{})
	enroll(first, secret, false)
	os.WriteFile(tokens, []byte("null"), 0600)
	enroll(second, secret, false)
	if registered, e := enrollment.ReadAuthorizedKeys(s.RegisteredKeys, false); e != nil || len(registered) != 1 {
		t.Fatal(registered, e)
	}
	snap := s.Snapshot()
	if !snap.EnrollEnabled || snap.Enrolled != 2 || snap.EnrollRejected != 5 {
		t.Fatal(snap)
	}
	// Corrupt one source as a whole. A valid key in the other remains authorized.
	os.WriteFile(keys, append(ssh.MarshalAuthorizedKey(second.PublicKey()), []byte("broken\n")...), 0600)
	if c, e := dial("hive", second); e == nil {
		c.Close()
		t.Fatal("accepted prefix of bad file")
	}
	c, e = dial("hive", first)
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	os.WriteFile(s.RegisteredKeys, nil, 0600)
	if c, e := dial("hive", first); e == nil {
		c.Close()
		t.Fatal("deleted key still authorized")
	}
}
func TestEnrollmentDisabledAndBudget(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	os.WriteFile(keys, nil, 0600)
	r, _ := registry.Open(filepath.Join(dir, "names"))
	s := New(r, keys)
	if _, e := s.enrollAuth(key(t).PublicKey()); e == nil {
		t.Fatal("disabled accepted")
	}
	s.Enrollment("tokens", "uses", "keys")
	if p, e := s.enrollAuth(key(t).PublicKey()); e != nil || p.Extensions["owner"] != "" {
		t.Fatal(p, e)
	}
	if cap(s.enrollSlots) != 4 {
		t.Fatal("wrong budget")
	}
}
