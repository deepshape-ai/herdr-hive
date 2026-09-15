package server

import (
	"context"
	"encoding/json"
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

func TestAPIChannelGenerationRolesAndUnpublish(t *testing.T) {
	dir := t.TempDir()
	host, owner, viewer := key(t), key(t), key(t)
	keys := filepath.Join(dir, "keys")
	os.WriteFile(keys, append(ssh.MarshalAuthorizedKey(owner.PublicKey()), ssh.MarshalAuthorizedKey(viewer.PublicKey())...), 0600)
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
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server leaked")
		}
	}()
	dial := func(user string, identity ssh.Signer) *ssh.Client {
		t.Helper()
		c, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(identity)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	publisher := dial("bee", owner)
	incoming := publisher.HandleChannelOpen(streamChannel)
	go func() {
		for n := range incoming {
			var req streamRequest
			if json.Unmarshal(n.ExtraData(), &req) != nil || req.Kind != "api" {
				t.Error("unexpected reverse operation")
			}
			ch, rs, err := n.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(rs)
			go func() {
				defer ch.Close()
				if req.Key == strings.Repeat("c", 32) {
					ch.Write([]byte(strings.Repeat("x", 64<<10)))
					return
				}
				io.Copy(ch, ch)
			}()
		}
	}()
	publish := func() []Share {
		t.Helper()
		p, _ := json.Marshal(Publish{Version: 1, Name: "B", Shares: []Share{
			{Key: strings.Repeat("a", 32), Label: "default", API: true, Generation: "caller-must-not-choose"},
			{Key: strings.Repeat("b", 32), Label: "legacy"},
			{Key: strings.Repeat("c", 32), Label: "oneshot", API: true},
		}})
		ok, data, err := publisher.SendRequest("publish@herdr-hive/v1", true, p)
		if err != nil || !ok {
			t.Fatal(string(data), err)
		}
		var shares []Share
		if json.Unmarshal(data, &shares) != nil {
			t.Fatal("invalid publication")
		}
		return shares
	}
	first := publish()
	if len(first[0].Generation) != 32 {
		t.Fatal("missing server generation")
	}
	consumer := dial("hive", viewer)
	open := func(c *ssh.Client, share Share, success bool) ssh.Channel {
		t.Helper()
		p, _ := json.Marshal(map[string]string{"share": share.ID, "generation": share.Generation})
		ch, rs, err := c.OpenChannel(apiChannel, p)
		if !success {
			if err == nil {
				ch.Close()
				t.Fatal("invalid target accepted")
			}
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(rs)
		return ch
	}
	open(consumer, first[1], false)            // Old Bee without API capability.
	open(dial("hive", owner), first[0], false) // Same-device hiding also gates API.
	open(publisher, first[0], false)           // A publisher transport cannot consume.
	missing := first[0]
	missing.ID = "s-missing"
	open(consumer, missing, false)
	// A one-response publisher closes immediately after writing. Draining SSH
	// request notifications must not truncate the queued API response bytes.
	for range 100 {
		ch := open(consumer, first[2], true)
		data, err := io.ReadAll(ch)
		ch.Close()
		if err != nil || len(data) != 64<<10 {
			t.Fatalf("one-shot response truncated: %d %v", len(data), err)
		}
	}
	active := open(consumer, first[0], true)
	defer active.Close()
	active.Write([]byte("one"))
	buf := make([]byte, 3)
	if _, err := io.ReadFull(active, buf); err != nil || string(buf) != "one" {
		t.Fatal(string(buf), err)
	}
	s.mu.Lock()
	bound := s.shares[first[0].ID]
	s.mu.Unlock()
	s.unpublish(bound.conn)
	ended := make(chan error, 1)
	go func() { _, err := active.Read(buf); ended <- err }()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("live stream survived revocation")
		}
	case <-time.After(time.Second):
		t.Fatal("revocation hung")
	}
	second := publish()
	if second[0].ID != first[0].ID || second[0].Generation == first[0].Generation {
		t.Fatal("invalid replacement identity")
	}
	open(consumer, first[0], false) // Existing CLI cannot follow the replacement.
	next := open(consumer, second[0], true)
	next.Close()
}
