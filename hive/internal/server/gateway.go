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

const maxGatewaySources = gateway.MaxSources

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
				out = append(out, gateway.Source{ID: b.share.ID, Name: b.share.Name, Label: b.share.Label, Generation: b.share.Generation, Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
					return s.openGatewaySource(ctx, b)
				}})
			}
			return out
		})
	}
	return errors.New("unsupported gateway request")
}

func (s *Server) openGatewaySource(ctx context.Context, b *binding) (io.ReadWriteCloser, error) {
	s.mu.Lock()
	if s.shares[b.share.ID] != b {
		s.mu.Unlock()
		return nil, errors.New("sharing changed")
	}
	if b.opening {
		s.mu.Unlock()
		return nil, errors.New("publisher channel opening is still pending")
	}
	b.opening = true
	s.mu.Unlock()

	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	type opened struct {
		channel  ssh.Channel
		requests <-chan *ssh.Request
		err      error
	}
	result := make(chan opened)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			b.opening = false
			s.mu.Unlock()
		}()
		payload, _ := json.Marshal(streamRequest{b.share.Key, "client"})
		channel, requests, err := b.conn.OpenChannel(streamChannel, payload)
		select {
		case result <- opened{channel, requests, err}:
		case <-openCtx.Done():
			if err == nil {
				done := make(chan struct{})
				go func() { defer close(done); ssh.DiscardRequests(requests) }()
				finishChannel(channel, done, func() { b.conn.Close() }, 2*time.Second)
			}
		}
	}()
	var open opened
	select {
	case open = <-result:
	case <-openCtx.Done():
		return nil, openCtx.Err()
	}
	if open.err != nil {
		return nil, open.err
	}
	done := make(chan struct{})
	wrapped := &gatewayStream{binding: b, Channel: open.channel, requestsDone: done, ctx: ctx}
	go func() { defer close(done); wrapped.readRequests(open.requests) }()
	s.mu.Lock()
	if s.shares[b.share.ID] != b {
		s.mu.Unlock()
		finishChannel(open.channel, done, func() { b.conn.Close() }, 2*time.Second)
		return nil, errors.New("sharing changed")
	}
	b.active[open.channel] = true
	s.gatewayUpstreams.Add(1)
	s.mu.Unlock()
	wrapped.close = func() {
		s.mu.Lock()
		delete(b.active, open.channel)
		s.gatewayUpstreams.Add(-1)
		s.mu.Unlock()
		finishChannel(open.channel, done, func() { b.conn.Close() }, 2*time.Second)
	}
	return wrapped, nil
}

type gatewayStream struct {
	binding *binding
	ssh.Channel
	once         sync.Once
	close        func()
	ctx          context.Context
	requestsDone <-chan struct{}
	failureMu    sync.Mutex
	failure      *gateway.UpstreamFailure
}

func (s *gatewayStream) Close() error { s.once.Do(s.close); return nil }

// Failure reports are bounded SSH sidebands, not native Herdr frames. Keeping
// request draining separate from data reads preserves SSH flow-control progress.
func (s *gatewayStream) readRequests(requests <-chan *ssh.Request) {
	for request := range requests {
		accepted := false
		if request.Type == "stream-failure@herdr-hive/v1" && len(request.Payload) <= 256 {
			var report struct {
				Code      string `json:"code"`
				Retryable *bool  `json:"retryable"`
			}
			if json.Unmarshal(request.Payload, &report) == nil && validFailureCode(report.Code) && report.Retryable != nil {
				s.failureMu.Lock()
				if s.failure == nil {
					s.failure = &gateway.UpstreamFailure{Code: report.Code, Retryable: *report.Retryable}
					accepted = true
				}
				s.failureMu.Unlock()
			}
		}
		if request.WantReply {
			request.Reply(accepted, nil)
		}
	}
}

func validFailureCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, c := range code {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

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
	timer := time.AfterFunc(10*time.Second, func() { s.Close() })
	defer timer.Stop()
	n, e := s.Channel.Write(p)
	s.binding.up.Add(uint64(n))
	return n, s.failureError(e)
}

func (s *gatewayStream) Read(p []byte) (int, error) {
	n, e := s.Channel.Read(p)
	s.binding.down.Add(uint64(n))
	return n, s.failureError(e)
}

func (s *gatewayStream) failureError(err error) error {
	if err == nil || s.requestsDone == nil {
		return err
	}
	// The peer queues its failure before CLOSE. Drain ordered requests on both
	// read and write failures so a concurrent writer cannot bypass quarantine.
	select {
	case <-s.requestsDone:
	case <-s.ctx.Done():
	default:
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-s.requestsDone:
		case <-s.ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	s.failureMu.Lock()
	failure := s.failure
	s.failureMu.Unlock()
	if failure != nil {
		return failure
	}
	return err
}
