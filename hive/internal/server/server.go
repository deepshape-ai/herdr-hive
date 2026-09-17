// Package server implements the application SSH gateway, without an OS shell.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/deepshape-ai/herdr-hive/hive/internal/enrollment"
	"github.com/deepshape-ai/herdr-hive/hive/internal/herdr"
	"github.com/deepshape-ai/herdr-hive/hive/internal/registry"
	"golang.org/x/crypto/ssh"
)

const maxControl = 64 << 10

// Herdr windows and transient machine-add checks each use one aggregate slot.
// Leave room for a small team while retaining a separate bounded memory budget.
const maxGatewayViewers = 8
const streamChannel = "bee-stream-v1"

type Share struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	API        bool   `json:"api,omitempty"`
	Generation string `json:"generation,omitempty"`
}
type Publish struct {
	Version int     `json:"version"`
	Name    string  `json:"name"`
	Shares  []Share `json:"shares"`
}
type streamRequest struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}
type binding struct {
	share  Share
	conn   *ssh.ServerConn
	active map[ssh.Channel]bool
	up     atomic.Uint64
	down   atomic.Uint64
}
type Server struct {
	Version, Executable                               string
	Registry                                          *registry.Registry
	AuthorizedKeys                                    string
	AuthorizedTokens, EnrollmentState, RegisteredKeys string
	enrollMu                                          sync.Mutex
	enrollSlots                                       chan struct{}
	gatewaySlots                                      chan struct{}
	enrolled, enrollRejected                          atomic.Uint64
	mu                                                sync.Mutex
	shares                                            map[string]*binding
	owners                                            map[string]*ssh.ServerConn
	conns                                             map[net.Conn]bool
	sem                                               chan struct{}
	wg                                                sync.WaitGroup
	channels                                          chan struct{}
	started                                           time.Time
	rejected                                          atomic.Uint64
	operationTimeout                                  time.Duration
}

func New(r *registry.Registry, keys string) *Server {
	return &Server{gatewaySlots: make(chan struct{}, maxGatewayViewers), enrollSlots: make(chan struct{}, 4), Registry: r, AuthorizedKeys: keys, shares: map[string]*binding{}, owners: map[string]*ssh.ServerConn{}, conns: map[net.Conn]bool{}, sem: make(chan struct{}, 64), channels: make(chan struct{}, 16), started: time.Now(), operationTimeout: 10 * time.Second}
}
func (s *Server) auth(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if _, certificate := key.(*ssh.Certificate); certificate {
		return nil, errors.New("SSH certificates unsupported")
	}
	if meta.User() == "enroll" {
		return s.enrollAuth(key)
	}
	external, e := enrollment.ReadAuthorizedKeys(s.AuthorizedKeys, false)
	// Each source fails closed independently; never accept a prefix of a corrupt file.
	matched := e == nil && enrollment.Contains(external, key)
	if s.RegisteredKeys != "" {
		registered, err := enrollment.ReadAuthorizedKeys(s.RegisteredKeys, true)
		matched = matched || (err == nil && enrollment.Contains(registered, key))
	}
	if matched {
		return &ssh.Permissions{Extensions: map[string]string{"owner": ssh.FingerprintSHA256(key)}}, nil
	}
	return nil, errors.New("unregistered key")
}
func (s *Server) Serve(ctx context.Context, l net.Listener, signer ssh.Signer) error {
	ctx, cancel := context.WithCancel(ctx)
	conf := &ssh.ServerConfig{PublicKeyCallback: s.auth, MaxAuthTries: 3, ServerVersion: "SSH-2.0-HerdrHive"}
	conf.AddHostKey(signer)
	go func() {
		<-ctx.Done()
		l.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
	}()
	defer func() { cancel(); s.wg.Wait() }()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case s.sem <- struct{}{}:
		default:
			s.rejected.Add(1)
			c.Close()
			continue
		}
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			c.Close()
			<-s.sem
			return nil
		}
		s.conns[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem; s.mu.Lock(); delete(s.conns, c); s.mu.Unlock(); c.Close() }()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			conn, chans, reqs, e := ssh.NewServerConn(&boundedConn{Conn: c}, conf)
			if e != nil {
				return
			}
			c.SetDeadline(time.Time{})
			if conn.User() == "enroll" {
				select {
				case s.enrollSlots <- struct{}{}:
				default:
					s.enrollRejected.Add(1)
					conn.Close()
					return
				}
				defer func() { <-s.enrollSlots }()
				timer := time.AfterFunc(15*time.Second, func() { c.Close() })
				defer timer.Stop()
			}
			closed := make(chan struct{})
			defer close(closed)
			go heartbeat(conn, closed)
			requestsDone := make(chan struct{})
			go func() { defer close(requestsDone); s.requests(conn, reqs) }()
			var handlers sync.WaitGroup
			defer func() { conn.Close(); <-requestsDone; s.unpublish(conn); handlers.Wait() }()
			slots := s.channels
			for ch := range chans {
				if conn.User() == "enroll" || (ch.ChannelType() != "session" && ch.ChannelType() != apiChannel) {
					ch.Reject(ssh.Prohibited, "only application sessions are supported")
					continue
				}
				select {
				case slots <- struct{}{}:
				default:
					s.rejected.Add(1)
					ch.Reject(ssh.ResourceShortage, "gateway channel limit")
					continue
				}
				handlers.Add(1)
				go func(ch ssh.NewChannel) {
					defer handlers.Done()
					defer func() { <-slots }()
					if ch.ChannelType() == apiChannel {
						s.api(conn, ch)
					} else {
						s.session(conn, ch)
					}
				}(ch)
			}
		}()
	}
}
func (s *Server) requests(c *ssh.ServerConn, reqs <-chan *ssh.Request) {
	if c.User() == "enroll" {
		s.enrollmentRequests(c, reqs)
		return
	}
	for r := range reqs {
		switch r.Type {
		case "enroll@herdr-hive/v1":
			s.enrollRejected.Add(1)
			r.Reply(false, nil)
		case "keepalive@openssh.com":
			r.Reply(true, nil)
		case "publish@herdr-hive/v1":
			if c.User() != "bee" || len(r.Payload) > maxControl {
				r.Reply(false, nil)
				continue
			}
			var p Publish
			e := json.Unmarshal(r.Payload, &p)
			if e == nil {
				var shares []Share
				shares, e = s.publish(c, p)
				if e == nil {
					b, _ := json.Marshal(shares)
					r.Reply(true, b)
					continue
				}
			}
			r.Reply(false, []byte("invalid or conflicting publication"))
		default:
			r.Reply(false, nil)
		}
	}
}

func validLabel(v string) bool {
	return len(v) > 0 && len(v) <= 128 && !strings.ContainsFunc(v, unicode.IsControl)
}

func (s *Server) publish(c *ssh.ServerConn, p Publish) ([]Share, error) {
	if p.Version != 1 || !validLabel(p.Name) || len(p.Shares) == 0 || len(p.Shares) > 32 {
		return nil, errors.New("invalid publication")
	}
	seen := map[string]bool{}
	for _, v := range p.Shares {
		if len(v.Key) != 32 || !validLabel(v.Label) || seen[v.Key] {
			return nil, errors.New("invalid share")
		}
		if _, e := hex.DecodeString(v.Key); e != nil {
			return nil, e
		}
		seen[v.Key] = true
	}
	owner := c.Permissions.Extensions["owner"]
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[owner] != nil {
		return nil, errors.New("device already publishing; use a separate key per device")
	}
	name, e := s.Registry.Name(owner, p.Name)
	if e != nil {
		return nil, e
	}
	out := make([]Share, 0, len(p.Shares))
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return nil, e
	}
	for _, v := range p.Shares {
		h := sha256.Sum256([]byte(owner + "\x00" + v.Key))
		v.ID = "s-" + hex.EncodeToString(h[:12])
		v.Name = name
		v.Generation = hex.EncodeToString(nonce[:])
		s.shares[v.ID] = &binding{share: v, conn: c, active: map[ssh.Channel]bool{}}
		out = append(out, v)
	}
	s.owners[owner] = c

	return out, nil
}
func (s *Server) unpublish(c *ssh.ServerConn) {
	var closeList []ssh.Channel
	s.mu.Lock()
	for id, b := range s.shares {
		if b.conn == c {
			for ch := range b.active {
				closeList = append(closeList, ch)
			}
			delete(s.shares, id)
		}
	}
	for id, owner := range s.owners {
		if owner == c {
			delete(s.owners, id)
		}
	}
	s.mu.Unlock()
	for _, ch := range closeList {
		ch.Close()
	}
}
func (s *Server) list() []Share {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Share{}
	for _, b := range s.shares {
		out = append(out, b.share)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Server) session(c *ssh.ServerConn, n ssh.NewChannel) {
	ch, reqs, e := n.Accept()
	if e != nil {
		return
	}
	var clientDone chan struct{}
	timer := time.AfterFunc(s.operationTimeout, func() { c.Close() })
	defer func() {
		timer.Stop()
		if clientDone == nil {
			clientDone = make(chan struct{})
			go func() { defer close(clientDone); ssh.DiscardRequests(reqs) }()
		}
		finishChannel(ch, clientDone, func() { c.Close() }, 2*time.Second)
	}()
	for r := range reqs {
		if r.Type != "exec" {
			r.Reply(false, nil)
			continue
		}
		var req struct{ Command string }
		if ssh.Unmarshal(r.Payload, &req) != nil || len(req.Command) > 4096 {
			r.Reply(false, nil)
			return
		}
		r.Reply(true, nil)
		code := uint32(0)
		clientDone = make(chan struct{})
		go func() { defer close(clientDone); ssh.DiscardRequests(reqs) }()
		e = s.execute(c, ch, req.Command, clientDone, timer)
		if e != nil {
			code = 1
			fmt.Fprintln(ch.Stderr(), "hive:", e)
		}
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
		return
	}
}
func (s *Server) execute(c *ssh.ServerConn, ch ssh.Channel, command string, clientDone <-chan struct{}, lifetime *time.Timer) error {
	if c.User() == "hive" {
		if command != "list --json" {
			return s.aggregate(c, ch, command, clientDone, lifetime)
		}
		visible := []Share{}
		owner := c.Permissions.Extensions["owner"]
		s.mu.Lock()
		for _, b := range s.shares {
			if b.conn.Permissions.Extensions["owner"] != owner {
				visible = append(visible, b.share)
			}
		}
		s.mu.Unlock()
		sort.Slice(visible, func(i, j int) bool { return visible[i].ID < visible[j].ID })
		return json.NewEncoder(ch).Encode(visible)
	}
	s.mu.Lock()
	b := s.shares[c.User()]
	if b != nil && b.conn.Permissions.Extensions["owner"] == c.Permissions.Extensions["owner"] {
		b = nil
	}
	if b != nil {
		b.active[ch] = true
	}
	s.mu.Unlock()
	if b == nil {
		return errors.New("shared session is offline")
	}
	defer func() { s.mu.Lock(); delete(b.active, ch); s.mu.Unlock() }()
	script := ""
	if command == "/bin/sh -s" {
		timer := time.AfterFunc(s.operationTimeout, func() { c.Close() })
		defer timer.Stop()
		data, e := io.ReadAll(io.LimitReader(ch, maxControl+1))
		if e != nil {
			return e
		}
		if len(data) > maxControl {
			return errors.New("bootstrap too large")
		}
		timer.Stop()
		script = string(data)
	}
	a, e := beginHerdrRequest(ch, command, script)
	if e != nil {
		return e
	}
	if a.Output != "" {
		_, e = io.WriteString(ch, a.Output)
		return e
	}
	if a.Kind == "" {
		return nil
	}
	payload, _ := json.Marshal(streamRequest{b.share.Key, a.Kind})
	timer := time.AfterFunc(10*time.Second, func() { b.conn.Close() })
	defer timer.Stop()
	remote, requests, e := b.conn.OpenChannel(streamChannel, payload)
	if e != nil {
		return errors.New("publisher unavailable or session changed")
	}
	timer.Stop()
	publisherDone := make(chan struct{})
	go func() { defer close(publisherDone); ssh.DiscardRequests(requests) }()
	defer finishChannel(remote, publisherDone, func() { b.conn.Close() }, 2*time.Second)
	if a.Kind == "client" {
		lifetime.Stop() // Streaming has no operation deadline; SSH flow control bounds queued data.
		return splice(ch, remote, &b.up, &b.down, clientDone, publisherDone, func() { c.Close() }, func() { b.conn.Close() })
	}
	_, e = io.Copy(ch, io.LimitReader(remote, maxControl))
	return e
}

func beginHerdrRequest(ch ssh.Channel, command, script string) (herdr.Action, error) {
	action, err := herdr.Resolve(command, script)
	if err != nil {
		return herdr.Action{}, err
	}
	if err = action.BeginResponse(ch); err != nil {
		return herdr.Action{}, err
	}
	return action, nil
}

func splice(a, b ssh.Channel, up, down *atomic.Uint64, clientDone, publisherDone <-chan struct{}, closeConsumer, closePublisher func()) error {
	done := make(chan error, 2)
	go func() { _, e := io.Copy(countWriter{b, up}, a); done <- e }()
	go func() { _, e := io.Copy(countWriter{a, down}, b); done <- e }()
	completed := 0
	var first error
	select {
	case first = <-done:
		completed = 1
	case <-clientDone:
	case <-publisherDone:
	}
	// Channel.Close needs the peer's CLOSE acknowledgement to wake SSH window waits.
	// Healthy peers acknowledge immediately. Escalate only a nonresponsive transport.
	consumerTimeout := time.AfterFunc(2*time.Second, closeConsumer)
	defer consumerTimeout.Stop()
	publisherTimeout := time.AfterFunc(4*time.Second, closePublisher)
	defer publisherTimeout.Stop()
	a.Close()
	b.Close()
	for completed < 2 {
		e := <-done
		if first == nil {
			first = e
		}
		completed++
	}
	return first
}

type countWriter struct {
	io.Writer
	n *atomic.Uint64
}

func (w countWriter) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	w.n.Add(uint64(n))
	return n, e
}

// Every network write has a deadline: a dead peer cannot hold locks indefinitely.
type boundedConn struct{ net.Conn }

func (c *boundedConn) Write(p []byte) (int, error) {
	c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.Conn.Write(p)
}

func heartbeat(c *ssh.ServerConn, closed <-chan struct{}) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-closed:
			return
		case <-tick.C:
			timer := time.AfterFunc(10*time.Second, func() { c.Close() })
			_, _, e := c.SendRequest("keepalive@openssh.com", true, nil)
			timer.Stop()
			if e != nil {
				c.Close()
				return
			}
		}
	}
}

// A completed operation retains its capacity slot until SSH channel state is reclaimed.
func finishChannel(ch ssh.Channel, done <-chan struct{}, closeTransport func(), timeout time.Duration) {
	timer := time.AfterFunc(timeout, closeTransport)
	defer timer.Stop()
	ch.Close()
	<-done
}
