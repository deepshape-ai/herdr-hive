package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestInvitationUsesPersistentHostKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := invitationHost("host.test:2222", dir); err == nil {
		t.Fatal("accepted uninitialized Hive")
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "host_key")
	os.WriteFile(path, pem.EncodeToMemory(block), 0600)
	pub, err := invitationHost("host.test:2222", dir)
	if err != nil {
		t.Fatal(err)
	}
	secret := "hreg-" + strings.Repeat("a", 64)
	raw := encodeInvitation("host.test:2222", pub, secret)
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "hinv1-"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Version              int `json:"version"`
		Hive, HostKey, Token string
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	json.Unmarshal(fields["version"], &v.Version)
	json.Unmarshal(fields["hive"], &v.Hive)
	json.Unmarshal(fields["host_key"], &v.HostKey)
	json.Unmarshal(fields["token"], &v.Token)
	if len(fields) != 4 || v.Version != 1 || v.Hive != "host.test:2222" || v.HostKey != pub || v.Token != secret {
		t.Fatal("wrong invitation fields")
	}
	if encodeInvitation("", "", secret) != "" {
		t.Fatal("changed legacy token output")
	}
	for _, host := range []string{"-oBad:22", "host:0", "bad\nHost:22", "[invalid:ip]:22", "host:65536"} {
		if _, err := invitationHost(host, dir); err == nil {
			t.Fatal("accepted invalid address")
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(pem.EncodeToMemory(block)) {
		t.Fatal("changed host identity")
	}
}
