// Package publisher owns registration and reverse streams, shared by TUI and CLI.
package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Share struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
}
type Status struct {
	Version    string  `json:"version,omitempty"`
	Executable string  `json:"executable,omitempty"`
	PID        int     `json:"pid,omitempty"`
	Enabled    bool    `json:"enabled"`
	Connected  bool    `json:"connected"`
	Shares     []Share `json:"shares"`
	Error      string  `json:"error,omitempty"`
}
type State struct {
	mu    sync.Mutex
	value Status
}

func (s *State) Get() Status  { s.mu.Lock(); defer s.mu.Unlock(); return s.value }
func (s *State) Set(v Status) { s.mu.Lock(); defer s.mu.Unlock(); s.value = v }

type idleConn struct{ net.Conn }

func (c idleConn) Read(b []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(45 * time.Second))
	return c.Conn.Read(b)
}
func (c idleConn) Write(b []byte) (int, error) {
	c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.Conn.Write(b)
}
func auth(ctx context.Context, c config.Config) (ssh.AuthMethod, func(), error) {
	b, e := os.ReadFile(c.IdentityFile)
	if e != nil {
		return nil, func() {}, e
	}
	signer, e := ssh.ParsePrivateKey(b)
	if e == nil {
		return ssh.PublicKeys(signer), func() {}, nil
	}
	var pass *ssh.PassphraseMissingError
	if !errors.As(e, &pass) {
		return nil, func() {}, e
	}
	sock, e := net.DialTimeout("unix", os.Getenv("SSH_AUTH_SOCK"), 3*time.Second)
	if e != nil {
		return nil, func() {}, errors.New("unlock the configured identity in your SSH agent")
	}
	sock.SetDeadline(time.Now().Add(5 * time.Second))
	stop := context.AfterFunc(ctx, func() { sock.Close() })
	cleanup := func() { stop(); sock.Close() }
	signers, e := agent.NewClient(sock).Signers()
	if e != nil {
		cleanup()
		return nil, func() {}, e
	}
	for _, s := range signers {
		if pass.PublicKey != nil && string(s.PublicKey().Marshal()) == string(pass.PublicKey.Marshal()) {
			return ssh.PublicKeys(s), cleanup, nil
		}
	}
	cleanup()
	return nil, func() {}, errors.New("configured identity is not unlocked in SSH agent")
}
func Run(ctx context.Context, c config.Config, state *State) {
	delay := time.Second
	for ctx.Err() == nil {
		e := connect(ctx, c, state)
		if ctx.Err() != nil {
			return
		}
		msg := "publisher disconnected"
		if e != nil {
			msg = e.Error()
		}
		state.Set(Status{Enabled: true, Error: msg})
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay *= 2
		}
	}
}
func connect(ctx context.Context, c config.Config, state *State) error {
	if len(c.Rules) == 0 {
		return errors.New("select a session before enabling sharing")
	}
	sessions, e := herdr.Sessions()
	if e != nil {
		return e
	}
	byName := map[string]herdr.Session{}
	for _, s := range sessions {
		byName[s.Name] = s
	}
	bound := map[string]herdr.Bound{}
	shares := []Share{}
	for _, r := range c.Rules {
		s, ok := byName[r.Session]
		if !ok {
			return fmt.Errorf("selected session %q is unavailable", r.Session)
		}
		b, e := herdr.Bind(s)
		if e != nil {
			return fmt.Errorf("session %q: %w", r.Session, e)
		}
		bound[r.Key] = b
		shares = append(shares, Share{Key: r.Key, Label: r.Session})
	}
	method, cleanup, e := auth(ctx, c)
	if e != nil {
		return e
	}
	defer cleanup()
	hostkey, e := knownhosts.New(c.KnownHosts)
	if e != nil {
		return e
	}
	raw, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", c.Hive)
	if e != nil {
		return e
	}
	defer raw.Close()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			raw.Close()
		case <-stopped:
		}
	}()
	conn, chans, reqs, e := ssh.NewClientConn(idleConn{raw}, c.Hive, &ssh.ClientConfig{User: "bee", Auth: []ssh.AuthMethod{method}, HostKeyCallback: hostkey, ClientVersion: "SSH-2.0-HerdrBee"})
	if e != nil {
		return e
	}
	client := ssh.NewClient(conn, chans, reqs)
	defer client.Close()
	incoming := client.HandleChannelOpen("bee-stream-v1")
	payload, _ := json.Marshal(struct {
		Version int     `json:"version"`
		Name    string  `json:"name"`
		Shares  []Share `json:"shares"`
	}{1, c.Name, shares})
	ok, b, e := client.SendRequest("publish@herdr-hive/v1", true, payload)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("Hive rejected publication (check identity, duplicate device, or protocol version)")
	}
	if e = json.Unmarshal(b, &shares); e != nil {
		return e
	}
	state.Set(Status{Enabled: true, Connected: true, Shares: shares})
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopped:
				return
			case <-tick.C:
				if _, _, e := client.SendRequest("keepalive@openssh.com", true, nil); e != nil {
					client.Close()
					return
				}
			}
		}
	}()
	slots := make(chan struct{}, 64)
	var wg sync.WaitGroup
	streamCtx, cancelStreams := context.WithCancel(ctx)
	defer wg.Wait()
	defer cancelStreams()
	// Close the SSH connection before waiting for handlers so every stream is interrupted.
	defer client.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-incoming:
			if !ok {
				return errors.New("Hive connection closed")
			}
			var req struct {
				Key  string `json:"key"`
				Kind string `json:"kind"`
			}
			if len(n.ExtraData()) > 4096 || json.Unmarshal(n.ExtraData(), &req) != nil {
				n.Reject(ssh.Prohibited, "invalid request")
				continue
			}
			target, ok := bound[req.Key]
			if !ok {
				n.Reject(ssh.Prohibited, "unshared session")
				continue
			}
			select {
			case slots <- struct{}{}:
			default:
				n.Reject(ssh.ResourceShortage, "publisher channel limit")
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-slots }(); handle(streamCtx, n, target, req.Kind) }()
		}
	}
}
func handle(ctx context.Context, n ssh.NewChannel, b herdr.Bound, kind string) {
	if kind == "client" || kind == "check" {
		local, e := b.Dial()
		if e != nil {
			n.Reject(ssh.ConnectionFailed, "session offline or replaced")
			return
		}
		defer local.Close()
		ch, reqs, e := n.Accept()
		if e != nil {
			return
		}
		defer ch.Close()
		if kind == "check" {
			go ssh.DiscardRequests(reqs)
			return
		}
		copyLocal(ctx, ch, reqs, local)

		return
	}
	if !strings.HasPrefix(kind, "status-") && kind != "platform" {
		n.Reject(ssh.Prohibited, "unsupported operation")
		return
	}
	data, e := b.Metadata(kind)
	if e != nil || len(data) > 64<<10 {
		n.Reject(ssh.ConnectionFailed, "Herdr metadata unavailable")
		return
	}
	ch, reqs, e := n.Accept()
	if e != nil {
		return
	}
	defer ch.Close()
	go ssh.DiscardRequests(reqs)
	ch.Write(data)
}

func copyLocal(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, local net.Conn) {
	stop := context.AfterFunc(ctx, func() { local.Close() })
	defer stop()
	go func() { ssh.DiscardRequests(reqs); local.Close() }()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(local, ch)
		if u, ok := local.(*net.UnixConn); ok {
			u.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() { io.Copy(ch, local); ch.CloseWrite(); done <- struct{}{} }()
	<-done
	local.Close()
	ch.Close()
	<-done
}
