package server

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"golang.org/x/crypto/ssh"
)

const apiChannel = "bee-api-v1"

// A generation pins every connection made by one CLI invocation to the same
// publication. A stable share ID alone would silently follow a reconnect.
func (s *Server) api(c *ssh.ServerConn, n ssh.NewChannel) {
	var req struct {
		Share      string `json:"share"`
		Generation string `json:"generation"`
	}
	d := json.NewDecoder(bytes.NewReader(n.ExtraData()))
	d.DisallowUnknownFields()
	if c.User() != "hive" || len(n.ExtraData()) > 4096 || d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.Generation == "" {
		n.Reject(ssh.Prohibited, "invalid Bee API target")
		return
	}
	s.mu.Lock()
	b := s.shares[req.Share]
	if b != nil && (!b.share.API || b.share.Generation != req.Generation || b.conn.Permissions.Extensions["owner"] == c.Permissions.Extensions["owner"]) {
		b = nil
	}
	s.mu.Unlock()
	if b == nil {
		n.Reject(ssh.Prohibited, "target offline, replaced, private, or API unsupported; rediscover with bee targets")
		return
	}
	payload, _ := json.Marshal(streamRequest{Key: b.share.Key, Kind: "api"})
	timer := time.AfterFunc(10*time.Second, func() { b.conn.Close() })
	remote, publisherRequests, err := b.conn.OpenChannel(streamChannel, payload)
	timer.Stop()
	if err != nil {
		n.Reject(ssh.ConnectionFailed, "publisher API unavailable; upgrade the target Bee if needed")
		return
	}
	publisherDone := make(chan struct{})
	go func() { defer close(publisherDone); ssh.DiscardRequests(publisherRequests) }()
	defer finishChannel(remote, publisherDone, func() { b.conn.Close() }, 2*time.Second)
	ch, consumerRequests, err := n.Accept()
	if err != nil {
		return
	}
	clientDone := make(chan struct{})
	go func() { defer close(clientDone); ssh.DiscardRequests(consumerRequests) }()
	defer finishChannel(ch, clientDone, func() { c.Close() }, 2*time.Second)
	s.mu.Lock()
	// Check again after opening the reverse channel; unpublish may have run.
	if s.shares[req.Share] != b {
		s.mu.Unlock()
		return
	}
	b.active[ch] = true
	b.active[remote] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(b.active, ch); delete(b.active, remote); s.mu.Unlock() }()
	relayAPI(ch, remote, b, c)
}

func relayAPI(consumer, publisher ssh.Channel, b *binding, c *ssh.ServerConn) {
	// Unlike an interactive terminal, an API publisher normally sends its
	// response and immediately closes. Request-channel closure can precede
	// draining the data buffer, so only data-copy completion ends this relay.
	done := make(chan struct{}, 2)
	go func() { io.Copy(countWriter{publisher, &b.up}, consumer); done <- struct{}{} }()
	go func() { io.Copy(countWriter{consumer, &b.down}, publisher); done <- struct{}{} }()
	<-done
	consumerTimeout := time.AfterFunc(2*time.Second, func() { c.Close() })
	defer consumerTimeout.Stop()
	publisherTimeout := time.AfterFunc(4*time.Second, func() { b.conn.Close() })
	defer publisherTimeout.Stop()
	consumer.Close()
	publisher.Close()
	<-done
}
