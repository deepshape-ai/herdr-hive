// Package enrollment stores administrator-issued tokens and service-owned usage.
package enrollment

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

const MaxFileBytes = 64 << 10
const MaxTokens = 1024
const MaxKeys = 1024
const Prefix = "hreg-"
const SecretBytes = 32

type Token struct {
	ID      string     `json:"id"`
	SHA256  string     `json:"sha256"`
	Created time.Time  `json:"created"`
	Expires *time.Time `json:"expires"`
	MaxUses int        `json:"max_uses"`
	Label   string     `json:"label"`
}
type Use struct {
	Uses int      `json:"uses"`
	Keys []string `json:"keys"`
}
type Uses map[string]Use

func Hash(secret string) string { h := sha256.Sum256([]byte(secret)); return hex.EncodeToString(h[:]) }
func validHex(s string, size int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == size && s == strings.ToLower(s)
}
func ValidateSecret(secret string) error {
	if !strings.HasPrefix(secret, Prefix) || !validHex(strings.TrimPrefix(secret, Prefix), SecretBytes) {
		return errors.New("invalid enrollment token")
	}
	return nil
}
func Generate(ttl time.Duration, maxUses int, label string) (Token, string, error) {
	if ttl < 0 || maxUses < 0 || len(label) > 128 || strings.ContainsFunc(label, unicode.IsControl) {
		return Token{}, "", errors.New("invalid token options")
	}
	b := make([]byte, SecretBytes)
	if _, e := rand.Read(b); e != nil {
		return Token{}, "", e
	}
	secret := Prefix + hex.EncodeToString(b)
	now := time.Now().UTC()
	t := Token{ID: "t-" + Hash(secret)[:16], SHA256: Hash(secret), Created: now, MaxUses: maxUses, Label: label}
	if ttl > 0 {
		expires := now.Add(ttl)
		t.Expires = &expires
	}
	return t, secret, nil
}
func read(path string, missing bool) ([]byte, error) {
	f, e := os.Open(path)
	if missing && os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > MaxFileBytes {
		return nil, errors.New("enrollment file exceeds 64 KiB")
	}
	return b, nil
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func validateTokens(ts []Token) error {
	if ts == nil || len(ts) > MaxTokens {
		return errors.New("invalid token list")
	}
	ids := map[string]bool{}
	hashes := map[string]bool{}
	for _, t := range ts {
		if !strings.HasPrefix(t.ID, "t-") || !validHex(strings.TrimPrefix(t.ID, "t-"), 8) || ids[t.ID] || hashes[t.SHA256] || !validHex(t.SHA256, 32) || t.Created.IsZero() || t.MaxUses < 0 || len(t.Label) > 128 || strings.ContainsFunc(t.Label, unicode.IsControl) {
			return errors.New("invalid token record")
		}
		ids[t.ID] = true
		hashes[t.SHA256] = true
	}
	return nil
}
func LoadTokens(path string) ([]Token, error) {
	b, e := read(path, false)
	if e != nil {
		return nil, e
	}
	var ts []Token
	if e = decode(b, &ts); e != nil {
		return nil, e
	}
	return ts, validateTokens(ts)
}
func SaveTokens(path string, ts []Token) error {
	if e := validateTokens(ts); e != nil {
		return e
	}
	return saveJSON(path, ts)
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, append(b, '\n'))
}

// ErrCommitted means replacement happened but directory durability could not be confirmed.
// Callers must not roll back a usage reservation after a key write returns this error.
var ErrCommitted = errors.New("replacement committed; directory sync failed")

func atomicWrite(path string, b []byte) error {
	if len(b) > MaxFileBytes {
		return errors.New("enrollment file exceeds 64 KiB")
	}
	// Keep an administrator-selected mode (e.g. 0644 for DynamicUser read access).
	mode := os.FileMode(0600)
	if fi, e := os.Stat(path); e == nil {
		mode = fi.Mode().Perm()
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".enroll-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(mode); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncDirectory(path)
}
func syncDirectory(path string) error {
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return fmt.Errorf("%w: %v", ErrCommitted, e)
	}
	defer dir.Close()
	if e = dir.Sync(); e != nil {
		return fmt.Errorf("%w: %v", ErrCommitted, e)
	}
	return nil
}

func LoadUses(path string) (Uses, error) {
	b, e := read(path, true)
	if e != nil {
		return nil, e
	}
	u := Uses{}
	if b == nil {
		return u, nil
	}
	if e = decode(b, &u); e != nil {
		return nil, e
	}
	if u == nil || len(u) > MaxTokens {
		return nil, errors.New("invalid usage state")
	}
	total := 0
	for id, v := range u {
		if !strings.HasPrefix(id, "t-") || !validHex(strings.TrimPrefix(id, "t-"), 8) || v.Uses < 0 || v.Uses != len(v.Keys) {
			return nil, errors.New("invalid usage record")
		}
		seen := map[string]bool{}
		for _, k := range v.Keys {
			if !strings.HasPrefix(k, "SHA256:") || len(k) != 50 || seen[k] {
				return nil, errors.New("invalid usage key")
			}
			seen[k] = true
		}
		total += len(v.Keys)
	}
	if total > MaxKeys {
		return nil, errors.New("too many usage keys")
	}
	return u, nil
}

// ReserveUse persists before granting access. A crash can reserve capacity, never grant an uncounted key.
// Retrying the same key resumes its reservation, including at the usage limit.
func ReserveUse(path string, t Token, key string) (bool, error) {
	u, e := LoadUses(path)
	if e != nil {
		return false, e
	}
	v := u[t.ID]
	for _, k := range v.Keys {
		if k == key {
			return false, syncDirectory(path)
		}
	}
	if t.MaxUses > 0 && v.Uses >= t.MaxUses {
		return false, errors.New("token exhausted")
	}
	if len(u) >= MaxTokens && v.Uses == 0 {
		return false, errors.New("usage limit")
	}
	total := 0
	for _, x := range u {
		total += len(x.Keys)
	}
	if total >= MaxKeys {
		return false, errors.New("usage key limit")
	}
	v.Uses++
	v.Keys = append(v.Keys, key)
	u[t.ID] = v
	return true, saveJSON(path, u)
}
func ReleaseUse(path, id, key string) error {
	u, e := LoadUses(path)
	if e != nil {
		return e
	}
	v := u[id]
	for i, k := range v.Keys {
		if k == key {
			v.Keys = append(v.Keys[:i], v.Keys[i+1:]...)
			v.Uses--
			break
		}
	}
	if v.Uses == 0 {
		delete(u, id)
	} else {
		u[id] = v
	}
	return saveJSON(path, u)
}
func ReadAuthorizedKeys(path string, missing bool) ([]ssh.PublicKey, error) {
	b, e := read(path, missing)
	if e != nil {
		return nil, e
	}
	keys := []ssh.PublicKey{}
	// Parse each non-comment line separately: ParseAuthorizedKey otherwise skips malformed lines.
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		k, _, opts, rest, e := ssh.ParseAuthorizedKey(line)
		if e != nil || len(opts) > 0 || len(bytes.TrimSpace(rest)) > 0 {
			return nil, errors.New("invalid authorized keys file")
		}
		if _, cert := k.(*ssh.Certificate); cert {
			return nil, errors.New("certificates unsupported")
		}
		keys = append(keys, k)
		if len(keys) > MaxKeys {
			return nil, errors.New("too many registered keys")
		}
	}
	return keys, nil
}
func Contains(keys []ssh.PublicKey, key ssh.PublicKey) bool {
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), key.Marshal()) {
			return true
		}
	}
	return false
}
func AppendAuthorizedKey(path string, key ssh.PublicKey, id string) error {
	keys, e := ReadAuthorizedKeys(path, true)
	if e != nil {
		return e
	}
	if Contains(keys, key) {
		return nil
	}
	if len(keys) >= MaxKeys {
		return errors.New("registered key limit")
	}
	b, e := read(path, true)
	if e != nil {
		return e
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + fmt.Sprintf(" enrolled=%s token=%s\n", time.Now().UTC().Format(time.RFC3339), id)
	return atomicWrite(path, append(b, []byte(line)...))
}
