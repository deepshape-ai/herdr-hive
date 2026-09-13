package server

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/deepshape-ai/herdr-hive/hive/internal/enrollment"
	"golang.org/x/crypto/ssh"
)

func (s *Server) Enrollment(tokens, state, keys string) {
	s.AuthorizedTokens = tokens
	s.EnrollmentState = state
	s.RegisteredKeys = keys
}
func (s *Server) enrollAuth(key ssh.PublicKey) (*ssh.Permissions, error) {
	if s.AuthorizedTokens == "" {
		s.enrollRejected.Add(1)
		return nil, errors.New("enrollment rejected")
	}
	switch key.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoRSA, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
	default:
		s.enrollRejected.Add(1)
		return nil, errors.New("enrollment rejected")
	}
	return &ssh.Permissions{Extensions: map[string]string{"enroll-pubkey": base64.StdEncoding.EncodeToString(key.Marshal())}}, nil
}
func (s *Server) enroll(c *ssh.ServerConn, secret string) (string, error) {
	s.enrollMu.Lock()
	defer s.enrollMu.Unlock()
	rejected := errors.New("enrollment rejected")
	if s.AuthorizedTokens == "" || enrollment.ValidateSecret(secret) != nil {
		return "", rejected
	}
	ts, e := enrollment.LoadTokens(s.AuthorizedTokens)
	if e != nil {
		return "", rejected
	}
	hash := enrollment.Hash(secret)
	var token *enrollment.Token
	for i := range ts {
		if subtle.ConstantTimeCompare([]byte(ts[i].SHA256), []byte(hash)) == 1 {
			token = &ts[i]
		}
	}
	if token == nil || (token.Expires != nil && !time.Now().Before(*token.Expires)) {
		return "", rejected
	}
	b, e := base64.StdEncoding.DecodeString(c.Permissions.Extensions["enroll-pubkey"])
	if e != nil {
		return "", rejected
	}
	key, e := ssh.ParsePublicKey(b)
	if e != nil {
		return "", rejected
	}
	registered, e := enrollment.ReadAuthorizedKeys(s.RegisteredKeys, true)
	if e != nil {
		return "", rejected
	}
	// Validate usage even for retries: corrupt service state cannot authorize enrollment.
	if _, e = enrollment.LoadUses(s.EnrollmentState); e != nil {
		return "", rejected
	}
	fingerprint := ssh.FingerprintSHA256(key)
	if enrollment.Contains(registered, key) {
		return fingerprint, nil
	}
	reserved, e := enrollment.ReserveUse(s.EnrollmentState, *token, fingerprint)
	if e != nil {
		return "", rejected
	}
	if e = enrollment.AppendAuthorizedKey(s.RegisteredKeys, key, token.ID); e != nil {
		if reserved && !errors.Is(e, enrollment.ErrCommitted) {
			_ = enrollment.ReleaseUse(s.EnrollmentState, token.ID, fingerprint)
		}
		return "", rejected
	}
	return fingerprint, nil
}

// Enrollment has one global request per connection and no channels or publication identity.
func (s *Server) enrollmentRequests(c *ssh.ServerConn, reqs <-chan *ssh.Request) {
	defer c.Close()
	r, ok := <-reqs
	if !ok {
		return
	}
	var p struct {
		Version int    `json:"version"`
		Token   string `json:"token"`
	}
	if r.Type != "enroll@herdr-hive/v1" || !r.WantReply || len(r.Payload) > maxControl || json.Unmarshal(r.Payload, &p) != nil || p.Version != 1 {
		s.enrollRejected.Add(1)
		r.Reply(false, nil)
		return
	}
	fingerprint, e := s.enroll(c, p.Token)
	if e != nil {
		s.enrollRejected.Add(1)
		r.Reply(false, nil)
		return
	}
	s.enrolled.Add(1)
	b, _ := json.Marshal(struct {
		Registered  bool   `json:"registered"`
		Fingerprint string `json:"fingerprint"`
	}{true, fingerprint})
	r.Reply(true, b)
}
