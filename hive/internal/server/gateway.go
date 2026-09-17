package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/gateway"
	"golang.org/x/crypto/ssh"
)

func (s *Server) aggregate(c *ssh.ServerConn, ch ssh.Channel, command string, clientDone <-chan struct{}, lifetime *time.Timer) error {
	script := ""
	if command == "/bin/sh -s" {
		data, e := io.ReadAll(io.LimitReader(ch, maxControl+1))
		if e != nil {
			return e
		}
		if len(data) > maxControl {
			return errors.New("bootstrap too large")
		}
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
	switch a.Kind {
	case "", "check":
		return nil
	case "platform":
		_, e = io.WriteString(ch, "Linux\nx86_64\n")
		return e
	case "status-client":
		return json.NewEncoder(ch).Encode(map[string]any{"version": "0.9.0", "channel": "stable", "protocol": 22, "endpoint_protocol_generation": 1, "endpoint_capabilities": []string{"surface_interest", "presentation_effects_fence", "health_check"}, "binary": "/herdr", "session": nil})
	case "status-server":
		return json.NewEncoder(ch).Encode(map[string]any{"status": "running", "running": true, "version": "0.9.0", "protocol": 22, "capabilities": map[string]any{"endpoint_protocol_generation": 1, "health_check": true, "surface_interest": true, "live_handoff": false, "detached_server_daemon": true}, "compatible": true, "endpoint_compatible": true, "socket": "", "session": nil, "restart_needed": false, "server_binary_stale": false})
	case "client":
		select {
		case s.gatewaySlots <- struct{}{}:
			defer func() { <-s.gatewaySlots }()
		default:
			return errors.New("gateway viewer capacity reached")
		}
		lifetime.Stop()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-clientDone:
				cancel()
			case <-ctx.Done():
			}
		}()
		return gateway.Serve(ctx, &deadlineStream{Channel: ch, stop: func() { c.Close() }}, func() []gateway.Source {
			owner := c.Permissions.Extensions["owner"]
			s.mu.Lock()
			defer s.mu.Unlock()
			out := []gateway.Source{}
			for _, b := range s.shares {
				if b.conn.Permissions.Extensions["owner"] == owner {
					continue
				}
				out = append(out, gateway.Source{ID: b.share.ID, Name: b.share.Name, Label: b.share.Label, Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
					payload, _ := json.Marshal(streamRequest{b.share.Key, "client"})
					timer := time.AfterFunc(10*time.Second, func() { b.conn.Close() })
					defer timer.Stop()
					remote, reqs, e := b.conn.OpenChannel(streamChannel, payload)
					if e != nil {
						return nil, e
					}
					timer.Stop()
					s.mu.Lock()
					if s.shares[b.share.ID] != b {
						s.mu.Unlock()
						remote.Close()
						return nil, errors.New("sharing changed")
					}
					b.active[remote] = true
					s.mu.Unlock()
					done := make(chan struct{})
					go func() { defer close(done); ssh.DiscardRequests(reqs) }()
					wrapped := &gatewayStream{binding: b, Channel: remote, stop: func() { b.conn.Close() }, close: func() {
						s.mu.Lock()
						delete(b.active, remote)
						s.mu.Unlock()
						finishChannel(remote, done, func() { b.conn.Close() }, 2*time.Second)
					}}
					go func() {
						select {
						case <-ctx.Done():
							wrapped.Close()
						case <-done:
						}
					}()
					return wrapped, nil
				}})
			}
			return out
		})
	}
	return errors.New("unsupported gateway request")
}

type gatewayStream struct {
	binding *binding
	ssh.Channel
	once  sync.Once
	stop  func()
	close func()
}

func (s *gatewayStream) Close() error { s.once.Do(s.close); return nil }

type deadlineStream struct {
	ssh.Channel
	stop func()
}

func (s *deadlineStream) Write(p []byte) (int, error) {
	timer := time.AfterFunc(10*time.Second, s.stop)
	defer timer.Stop()
	return s.Channel.Write(p)
}
func (s *gatewayStream) Write(p []byte) (int, error) {
	timer := time.AfterFunc(10*time.Second, s.stop)
	defer timer.Stop()
	n, e := s.Channel.Write(p)
	s.binding.up.Add(uint64(n))
	return n, e
}

func (s *gatewayStream) Read(p []byte) (int, error) {
	n, e := s.Channel.Read(p)
	s.binding.down.Add(uint64(n))
	return n, e
}
