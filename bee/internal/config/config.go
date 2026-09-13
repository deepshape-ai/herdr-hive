// Package config owns the versioned publisher configuration. Private keys are referenced, not copied.
package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

type Rule struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Key     string `json:"key"`
}
type Config struct {
	Version      int    `json:"version"`
	Hive         string `json:"hive"`
	IdentityFile string `json:"identity_file"`
	KnownHosts   string `json:"known_hosts"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Rules        []Rule `json:"rules"`
}

func Dir() string {
	if d := os.Getenv("BEE_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	// Resolve the same directory used by plugin actions when called directly from a shell.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if b, e := exec.CommandContext(ctx, "herdr", "plugin", "config-dir", "herdr.bee").Output(); e == nil {
		if p := strings.TrimSpace(string(b)); filepath.IsAbs(p) {
			return p
		}
	}
	d, e := os.UserConfigDir()
	if e != nil {
		return ".bee"
	}
	return filepath.Join(d, "herdr-bee")
}
func Load(dir string) (Config, error) {
	name, _ := os.Hostname()
	c := Config{Version: 1, Name: name, Rules: []Rule{}}
	f, e := os.Open(filepath.Join(dir, "config.json"))
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if len(b) > 65536 {
		return c, errors.New("configuration exceeds 64 KiB")
	}
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&c); e != nil {
		return c, e
	}
	if e = dec.Decode(new(any)); e != io.EOF {
		return c, errors.New("configuration must contain exactly one JSON object")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Version != 1 {
		return errors.New("unsupported configuration version; upgrade Bee before modifying it")
	}
	if c.Name == "" || len(c.Name) > 128 || strings.ContainsFunc(c.Name, unicode.IsControl) {
		return errors.New("name must contain 1–128 bytes without control characters")
	}
	if len(c.Rules) > 32 {
		return errors.New("at most 32 sessions per publishing device")
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if r.Type != "manual" {
			return fmt.Errorf("unsupported sharing rule %q; upgrade Bee", r.Type)
		}
		if r.Session == "" || seen[r.Session] || len(r.Key) != 32 {
			return errors.New("invalid or duplicate manual rule")
		}
		if _, e := hex.DecodeString(r.Key); e != nil {
			return e
		}
		seen[r.Session] = true
	}
	return nil
}
func Update(dir string, fn func(*Config) error) (Config, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return Config{}, e
	}
	l, e := os.OpenFile(filepath.Join(dir, "config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return Config{}, e
	}
	defer l.Close()
	if e = syscall.Flock(int(l.Fd()), syscall.LOCK_EX); e != nil {
		return Config{}, e
	}
	c, e := Load(dir)
	if e != nil {
		return c, e
	}
	if e = fn(&c); e != nil {
		return c, e
	}
	if e = c.Validate(); e != nil {
		return c, e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return c, e
	}
	f, e := os.CreateTemp(dir, ".config-*")
	if e != nil {
		return c, e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), filepath.Join(dir, "config.json"))
	}
	return c, e
}
func NewRule(session string) (Rule, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return Rule{}, e
	}
	return Rule{Type: "manual", Session: session, Key: hex.EncodeToString(b[:])}, nil
}
