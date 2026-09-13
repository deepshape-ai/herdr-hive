package publisher

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func dial(ctx context.Context, c config.Config, user, version string) (*ssh.Client, func(), error) {
	noop := func() {}
	method, authCleanup, e := auth(ctx, c)
	if e != nil {
		return nil, noop, e
	}
	hostkey, e := knownhosts.New(c.KnownHosts)
	if e != nil {
		authCleanup()
		return nil, noop, e
	}
	raw, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", c.Hive)
	if e != nil {
		authCleanup()
		return nil, noop, e
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	cleanup := func() { stop(); raw.Close(); authCleanup() }
	// Bound the handshake even if the peer keeps sending partial packets.
	timer := time.AfterFunc(10*time.Second, func() { raw.Close() })
	conn, chans, reqs, e := ssh.NewClientConn(idleConn{raw}, c.Hive, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{method}, HostKeyCallback: hostkey, ClientVersion: version})
	timer.Stop()
	if e != nil {
		cleanup()
		return nil, noop, e
	}
	client := ssh.NewClient(conn, chans, reqs)
	return client, func() { client.Close(); cleanup() }, nil
}

// Enroll proves possession of the configured key, then presents the token inside verified SSH.
func Enroll(ctx context.Context, c config.Config, token string) (string, error) {
	secret, e := hex.DecodeString(strings.TrimPrefix(token, "hreg-"))
	if !strings.HasPrefix(token, "hreg-") || e != nil || len(secret) != 32 || token != strings.ToLower(token) {
		return "", errors.New("invalid enrollment token")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, cleanup, e := dial(ctx, c, "enroll", "SSH-2.0-HerdrBeeEnroll")
	if e != nil {
		return "", e
	}
	defer cleanup()
	payload, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Token   string `json:"token"`
	}{1, token})
	ok, b, e := client.SendRequest("enroll@herdr-hive/v1", true, payload)
	if e != nil {
		return "", errors.New("enrollment connection failed")
	}
	if !ok {
		return "", errors.New("Hive rejected enrollment")
	}
	var result struct {
		Registered  bool   `json:"registered"`
		Fingerprint string `json:"fingerprint"`
	}
	if len(b) > 64<<10 || json.Unmarshal(b, &result) != nil || !result.Registered || !strings.HasPrefix(result.Fingerprint, "SHA256:") || len(result.Fingerprint) != 50 {
		return "", errors.New("invalid enrollment response")
	}
	return result.Fingerprint, nil
}
