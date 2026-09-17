package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/registry"
	"golang.org/x/crypto/ssh"
)

func key(t *testing.T) ssh.Signer {
	t.Helper()
	_, p, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	s, e := ssh.NewSignerFromKey(p)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func TestGatewayDuplexLimitsBackpressureAndDisconnect(t *testing.T) {
	dir := t.TempDir()
	host, identity := key(t), key(t)
	viewerIdentity := key(t)
	keys := filepath.Join(dir, "authorized_keys")
	os.WriteFile(keys, append(ssh.MarshalAuthorizedKey(identity.PublicKey()), ssh.MarshalAuthorizedKey(viewerIdentity.PublicKey())...), 0600)
	r, e := registry.Open(filepath.Join(dir, "names.json"))
	if e != nil {
		t.Fatal(e)
	}
	gateway := New(r, keys)
	gateway.Limits(8, 1)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gateway.Serve(ctx, l, host) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to stop")
		}
	}()
	dial := func(user string) *ssh.Client {
		t.Helper()
		signer := identity
		if user != "bee" {
			signer = viewerIdentity
		}
		c, e := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	unknown := key(t)
	if c, e := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "hive", Auth: []ssh.AuthMethod{ssh.PublicKeys(unknown)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second}); e == nil {
		c.Close()
		t.Fatal("unregistered device authenticated")
	}
	bee := dial("bee")
	defer bee.Close()
	incoming := bee.HandleChannelOpen(streamChannel)
	keyID := strings.Repeat("a", 32)
	p, _ := json.Marshal(Publish{Version: 1, Name: "Worker", Shares: []Share{{Key: keyID, Label: "default"}}})
	ok, data, e := bee.SendRequest("publish@herdr-hive/v1", true, p)
	if e != nil || !ok {
		t.Fatalf("publish %s %v", data, e)
	}
	var shares []Share
	json.Unmarshal(data, &shares)
	consumer := dial(shares[0].ID)
	defer consumer.Close()
	start := func(command string) ssh.Channel {
		t.Helper()
		ch, requests, e := consumer.OpenChannel("session", nil)
		if e != nil {
			t.Fatal(e)
		}
		go ssh.DiscardRequests(requests)
		ok, e := ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{command}))
		if e != nil || !ok {
			t.Fatal(e)
		}
		return ch
	}
	ch := start("printf '\n%s\n' 'herdr-remote-output-ready:1'\nexec /herdr remote-client-bridge")
	prelude := make([]byte, len("\nherdr-remote-output-ready:1\n"))
	if _, e = io.ReadFull(ch, prelude); e != nil || string(prelude) != "\nherdr-remote-output-ready:1\n" {
		t.Fatalf("framed prelude %q %v", prelude, e)
	}
	n := <-incoming
	remote, reqs, e := n.Accept()
	if e != nil {
		t.Fatal(e)
	}
	go ssh.DiscardRequests(reqs)
	go remote.Write([]byte("screen"))
	buf := make([]byte, 6)
	if _, e = io.ReadFull(ch, buf); e != nil || string(buf) != "screen" {
		t.Fatalf("screen %q %v", buf, e)
	}
	go ch.Write([]byte("input"))
	buf = make([]byte, 5)
	if _, e = io.ReadFull(remote, buf); e != nil || string(buf) != "input" {
		t.Fatalf("input %q %v", buf, e)
	}
	if extra, _, e := consumer.OpenChannel("session", nil); e == nil {
		extra.Close()
		t.Fatal("global channel limit not enforced")
	}
	ch.Close()
	remote.Close()
	eventually(t, func() bool { return gateway.Snapshot().Channels == 0 })
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	slow := start("exec /herdr remote-client-bridge")
	n = <-incoming
	producer, reqs, e := n.Accept()
	if e != nil {
		t.Fatal(e)
	}
	go ssh.DiscardRequests(reqs)
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		block := bytes.Repeat([]byte("x"), 32<<10)
		for i := 0; i < 4096; i++ {
			if _, e := producer.Write(block); e != nil {
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	runtime.GC()
	runtime.ReadMemStats(&after)
	select {
	case <-produced:
		t.Fatal("128 MiB producer was not backpressured")
	default:
	}
	// Includes client/server buffers in this process, not only gateway allocations.
	if after.HeapAlloc > before.HeapAlloc+32<<20 {
		t.Fatalf("unexpected heap growth: %d", after.HeapAlloc-before.HeapAlloc)
	}
	// Keep Bee connected while the consumer alone disappears under output backpressure.
	consumer.Close()
	eventually(t, func() bool { return gateway.Snapshot().Channels == 0 })
	if len(gateway.list()) != 1 {
		t.Fatal("consumer disconnect dropped the publisher")
	}
	producer.Close()
	consumer = dial(shares[0].ID)
	stalled := start("exec /herdr remote-client-bridge")
	n = <-incoming
	noReader, reqs, e := n.Accept()
	if e != nil {
		t.Fatal(e)
	}
	go ssh.DiscardRequests(reqs)
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		block := bytes.Repeat([]byte("i"), 32<<10)
		for i := 0; i < 4096; i++ {
			if _, e := stalled.Write(block); e != nil {
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	consumer.Close()
	eventually(t, func() bool { return gateway.Snapshot().Channels == 0 })
	if len(gateway.list()) != 1 {
		t.Fatal("stalled input disconnected healthy Bee")
	}
	noReader.Close()
	stalled.Close()
	<-inputDone
	consumer = dial(shares[0].ID)
	bee.Close()
	slow.Close()
	producer.Close()
	eventually(t, func() bool { return len(gateway.list()) == 0 && gateway.Snapshot().Channels == 0 })
	select {
	case <-produced:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked producer leaked")
	}
	if gateway.Snapshot().Rejected == 0 {
		t.Fatal("missing resource rejection metric")
	}
	// Existing consumer transport cannot reconnect to a publication that disappeared.
	session, e := consumer.NewSession()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = session.CombinedOutput("command -v herdr"); e == nil {
		t.Fatal("offline publication accepted")
	}
	session.Close()
}

func TestIncompleteRequestsCloseConsumerTransport(t *testing.T) {
	dir := t.TempDir()
	host, identity := key(t), key(t)
	keys := filepath.Join(dir, "keys")
	os.WriteFile(keys, ssh.MarshalAuthorizedKey(identity.PublicKey()), 0600)
	r, _ := registry.Open(filepath.Join(dir, "names.json"))
	gateway := New(r, keys)
	gateway.operationTimeout = 100 * time.Millisecond
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gateway.Serve(ctx, l, host) }()
	defer func() { cancel(); <-done }()
	c, e := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "hive", Auth: []ssh.AuthMethod{ssh.PublicKeys(identity)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey())})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	channel, requests, e := c.OpenChannel("session", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	closed := make(chan error, 1)
	go func() { closed <- c.Wait() }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle session only closed its channel, not the offending transport")
	}
	eventually(t, func() bool { return gateway.Snapshot().Channels == 0 })
}

type closeOnlyChannel struct {
	ssh.Channel
	sent chan struct{}
}

func (c closeOnlyChannel) Close() error { close(c.sent); return nil }
func TestCompletedChannelWaitsForCloseAcknowledgement(t *testing.T) {
	acknowledged := make(chan struct{})
	sent := make(chan struct{})
	finished := make(chan struct{})
	forced := make(chan struct{})
	go func() {
		finishChannel(closeOnlyChannel{sent: sent}, acknowledged, func() { close(forced); close(acknowledged) }, 100*time.Millisecond)
		close(finished)
	}()
	<-sent
	select {
	case <-finished:
		t.Fatal("released capacity before close acknowledgement")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("close-ack timeout did not terminate transport")
	}
	select {
	case <-forced:
	default:
		t.Fatal("offending transport was not closed")
	}
}
