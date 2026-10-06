package server

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/gateway"
	"golang.org/x/crypto/ssh"
)

func publicationPeer(t *testing.T) (*Server, *binding, <-chan ssh.NewChannel) {
	t.Helper()
	host := key(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	type accepted struct {
		conn *ssh.ServerConn
		err  error
	}
	ready := make(chan accepted, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			ready <- accepted{err: err}
			return
		}
		config := &ssh.ServerConfig{NoClientAuth: true}
		config.AddHostKey(host)
		conn, channels, requests, err := ssh.NewServerConn(raw, config)
		ready <- accepted{conn: conn, err: err}
		if err == nil {
			go ssh.DiscardRequests(requests)
			for channel := range channels {
				channel.Reject(ssh.Prohibited, "no inbound fixture channels")
			}
		}
	}()
	peer, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User: "bee", HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	server := <-ready
	if server.err != nil {
		t.Fatal(server.err)
	}
	t.Cleanup(func() { server.conn.Close() })
	b := &binding{conn: server.conn, share: Share{ID: "fixture", Key: strings.Repeat("a", 32), Generation: strings.Repeat("b", 32)}, active: map[ssh.Channel]bool{}}
	s := &Server{shares: map[string]*binding{b.share.ID: b}}
	t.Cleanup(func() { server.conn.Close(); s.wg.Wait() })
	return s, b, peer.HandleChannelOpen(streamChannel)
}

func acceptGatewayStream(t *testing.T, s *Server, b *binding, incoming <-chan ssh.NewChannel) (*gatewayStream, ssh.Channel) {
	t.Helper()
	type result struct {
		stream io.ReadWriteCloser
		err    error
	}
	opened := make(chan result, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { stream, err := s.openGatewaySource(ctx, b); opened <- result{stream, err} }()
	var pending ssh.NewChannel
	select {
	case pending = <-incoming:
	case <-time.After(3 * time.Second):
		t.Fatal("reverse channel did not open")
	}
	peer, requests, err := pending.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(requests)
	t.Cleanup(func() { peer.Close() })
	out := <-opened
	if out.err != nil {
		t.Fatal(out.err)
	}
	stream := out.stream.(*gatewayStream)
	t.Cleanup(func() { stream.Close() })
	return stream, peer
}

func TestGatewayFailureReportSurvivesChannelClosure(t *testing.T) {
	s, b, incoming := publicationPeer(t)
	stream, peer := acceptGatewayStream(t, s, b, incoming)
	// The sender does not wait for a reply: EOF must not outrun sideband parsing.
	if _, err := peer.SendRequest("stream-failure@herdr-hive/v1", false, []byte(`{"code":"sizing_unavailable","retryable":false}`)); err != nil {
		t.Fatal(err)
	}
	peer.Close()
	_, err := stream.Read(make([]byte, 1))
	var failure *gateway.UpstreamFailure
	if !errors.As(err, &failure) || failure.Code != "sizing_unavailable" || failure.Retryable {
		t.Fatalf("closed source lost persistent failure: %v", err)
	}
	_, err = stream.Write([]byte{20})
	if !errors.As(err, &failure) || failure.Code != "sizing_unavailable" {
		t.Fatalf("concurrent writer bypassed persistent failure: %v", err)
	}
}

func TestGatewayRejectsMalformedFailureReports(t *testing.T) {
	for _, payload := range []string{
		`{"code":"/private/native/path","retryable":false}`,
		`{"code":"sizing_unavailable"}`,
		`{"code":"sizing_unavailable","retryable":"false"}`,
		`{"code":"","retryable":false}`,
		strings.Repeat("x", 257),
	} {
		t.Run(payload, func(t *testing.T) {
			s, b, incoming := publicationPeer(t)
			stream, peer := acceptGatewayStream(t, s, b, incoming)
			accepted, err := peer.SendRequest("stream-failure@herdr-hive/v1", true, []byte(payload))
			if err != nil || accepted {
				t.Fatalf("invalid failure accepted: %v, %v", accepted, err)
			}
			peer.Close()
			_, err = stream.Read(make([]byte, 1))
			var failure *gateway.UpstreamFailure
			if errors.As(err, &failure) {
				t.Fatalf("malformed report quarantined a source: %v", err)
			}
		})
	}
}

func TestCanceledGatewayOpenDoesNotClosePublisherOrAccumulateWorkers(t *testing.T) {
	s, b, incoming := publicationPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := s.openGatewaySource(ctx, b); finished <- err }()
	pending := <-incoming
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open = %v", err)
	}
	if _, err := s.openGatewaySource(context.Background(), b); err == nil {
		t.Fatal("a second worker was admitted while the peer withheld OPEN confirmation")
	}
	other := &binding{conn: b.conn, share: Share{ID: "other", Key: strings.Repeat("c", 32)}, active: map[ssh.Channel]bool{}}
	s.mu.Lock()
	s.shares[other.share.ID] = other
	s.mu.Unlock()
	stream, peer := acceptGatewayStream(t, s, other, incoming)
	if accepted, err := peer.SendRequest("stream-failure@herdr-hive/v1", true, []byte(`{"code":"temporary_transport","retryable":true}`)); err != nil || !accepted {
		t.Fatalf("sibling stream no longer usable: %v, %v", accepted, err)
	}
	peer.Close()
	stream.Close()
	late, requests, err := pending.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(requests)
	defer late.Close()
	eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !b.opening
	})
}
