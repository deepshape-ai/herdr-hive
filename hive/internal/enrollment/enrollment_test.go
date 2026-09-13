package enrollment

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func publicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	p, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	k, e := ssh.NewPublicKey(p)
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func TestTokens(t *testing.T) {
	a, secret, e := Generate(0, 0, "onboarding")
	if e != nil || ValidateSecret(secret) != nil || a.Expires != nil || a.SHA256 != Hash(secret) {
		t.Fatal(a, e)
	}
	b, other, e := Generate(time.Hour, 2, "limited")
	if e != nil || secret == other || a.ID == b.ID || b.Expires == nil {
		t.Fatal(b, e)
	}
	for _, s := range []string{"", "hreg-", strings.Repeat("a", 64), "hreg-" + strings.Repeat("G", 64), secret + "a"} {
		if ValidateSecret(s) == nil {
			t.Fatal("accepted", s)
		}
	}
	for _, args := range []struct {
		ttl   time.Duration
		n     int
		label string
	}{{-1, 0, ""}, {0, -1, ""}, {0, 0, "bad\n"}} {
		if _, _, e := Generate(args.ttl, args.n, args.label); e == nil {
			t.Fatal("invalid generation")
		}
	}
	path := filepath.Join(t.TempDir(), "tokens")
	if _, e := LoadTokens(path); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if e := SaveTokens(path, []Token{a, b}); e != nil {
		t.Fatal(e)
	}
	got, e := LoadTokens(path)
	if e != nil || len(got) != 2 {
		t.Fatal(got, e)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("stored secret")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0600 {
		t.Fatal(fi.Mode())
	}
	os.Chmod(path, 0644)
	if e := SaveTokens(path, []Token{a}); e != nil {
		t.Fatal(e)
	}
	fi, _ = os.Stat(path)
	if fi.Mode().Perm() != 0644 {
		t.Fatal("lost service read permission")
	}
	if SaveTokens(path, []Token{a, a}) == nil || SaveTokens(path, nil) == nil {
		t.Fatal("accepted duplicate/null")
	}
	for _, data := range []string{"null", "", "{}", "[] {}", "[", strings.Repeat(" ", MaxFileBytes+1)} {
		os.WriteFile(path, []byte(data), 0600)
		if _, e := LoadTokens(path); e == nil {
			t.Fatal("accepted bad file")
		}
	}
	a.MaxUses = -1
	if SaveTokens(path, []Token{a}) == nil {
		t.Fatal("negative uses")
	}
}
func TestAuthorizedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys")
	k := publicKey(t)
	if e := AppendAuthorizedKey(path, k, "t-0123456789abcdef"); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(path)
	if e := AppendAuthorizedKey(path, k, "t-0123456789abcdef"); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("non-idempotent")
	}
	keys, e := ReadAuthorizedKeys(path, false)
	if e != nil || len(keys) != 1 || !Contains(keys, k) {
		t.Fatal(keys, e)
	}
	for _, data := range [][]byte{append(before, []byte("broken\n")...), append([]byte("bad\n"), before...), append([]byte("restrict "), ssh.MarshalAuthorizedKey(k)...), bytes.Repeat([]byte(" "), MaxFileBytes+1)} {
		os.WriteFile(path, data, 0600)
		if _, e := ReadAuthorizedKeys(path, false); e == nil {
			t.Fatal("accepted corrupt keys")
		}
		if AppendAuthorizedKey(path, publicKey(t), "t-0123456789abcdef") == nil {
			t.Fatal("appended to corrupt file")
		}
	}
	os.WriteFile(path, bytes.Repeat(ssh.MarshalAuthorizedKey(k), 900), 0600)
	before, _ = os.ReadFile(path)
	if AppendAuthorizedKey(path, publicKey(t), "t-0123456789abcdef") == nil {
		t.Fatal("accepted oversized keys")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("changed failed append")
	}
}
func TestUsageReservationRetryAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uses")
	token, _, _ := Generate(0, 1, "")
	a, b := ssh.FingerprintSHA256(publicKey(t)), ssh.FingerprintSHA256(publicKey(t))
	if added, e := ReserveUse(path, token, a); !added || e != nil {
		t.Fatal(added, e)
	}
	if added, e := ReserveUse(path, token, a); added || e != nil {
		t.Fatal(added, e)
	}
	if _, e := ReserveUse(path, token, b); e == nil {
		t.Fatal("exceeded limit")
	}
	if e := ReleaseUse(path, token.ID, a); e != nil {
		t.Fatal(e)
	}
	if _, e := ReserveUse(path, token, b); e != nil {
		t.Fatal(e)
	}
	u, e := LoadUses(path)
	if e != nil || u[token.ID].Uses != 1 {
		t.Fatal(u, e)
	}
	for _, data := range []string{"null", "{} {}", `{"t-0123456789abcdef":{"uses":2,"keys":[]}}`, strings.Repeat(" ", MaxFileBytes+1)} {
		os.WriteFile(path, []byte(data), 0600)
		if _, e := ReserveUse(path, token, a); e == nil {
			t.Fatal("accepted corrupt usage")
		}
	}
}
