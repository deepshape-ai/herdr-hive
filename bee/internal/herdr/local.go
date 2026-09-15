// Package herdr is Bee's only local Herdr integration point.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Session struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Socket  string `json:"socket_path"`
}
type Bound struct {
	Session
	clientPath          string
	apiInfo, clientInfo os.FileInfo
	sizing              *Sizing
	lifetime            *boundLifetime
}

// Bound values are copied into handlers and sizing helpers. They share one
// lifetime so closing any copy releases each endpoint pin exactly once.
type boundLifetime struct {
	sync.RWMutex
	closed bool
	pins   []*os.File
}

func binary() string {
	if v := os.Getenv("BEE_HERDR_BINARY"); v != "" {
		return v
	}
	if v := os.Getenv("HERDR_BIN_PATH"); v != "" {
		return v
	}
	return "herdr"
}
func cleanEnv() []string {
	out := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "HERDR_") {
			out = append(out, v)
		}
	}
	return out
}
func command(ctx context.Context, socket string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary(), args...)
	cmd.Env = cleanEnv()
	if socket != "" {
		cmd.Env = append(cmd.Env, "HERDR_SOCKET_PATH="+socket)
	}
	return cmd.Output()
}
func Sessions() ([]Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, e := command(ctx, "", "session", "list", "--json")
	if e != nil {
		return nil, e
	}
	var list struct {
		Sessions []Session `json:"sessions"`
	}
	e = json.Unmarshal(b, &list)
	return list.Sessions, e
}
func Bind(s Session) (b Bound, err error) {
	b = Bound{Session: s, lifetime: &boundLifetime{}}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()
	if !s.Running || !filepath.IsAbs(s.Socket) {
		return b, errors.New("session is not running")
	}
	ext := filepath.Ext(s.Socket)
	b.clientPath = strings.TrimSuffix(s.Socket, ext) + "-client" + ext
	for _, endpoint := range []struct {
		path string
		info *os.FileInfo
	}{{s.Socket, &b.apiInfo}, {b.clientPath, &b.clientInfo}} {
		info, pin, e := pinEndpoint(endpoint.path)
		if e != nil {
			return b, e
		}
		*endpoint.info = info
		if pin != nil {
			b.lifetime.pins = append(b.lifetime.pins, pin)
		}
	}
	if b.apiInfo.Mode()&os.ModeSocket == 0 || b.clientInfo.Mode()&os.ModeSocket == 0 {
		return b, errors.New("Herdr endpoints must be Unix sockets")
	}
	// Pinning and path lookup are separate operations. Reject replacements
	// that happened while the two endpoint identities were being collected.
	if err := b.Check(); err != nil {
		return b, err
	}
	b.sizing = &Sizing{bound: b, locks: map[string]*sizeLock{}}
	return b, nil
}

// Close releases the publication's endpoint pins. It is safe on copied Bound
// values and repeated calls. The owner must close after its handlers finish.
func (b Bound) Close() {
	if b.lifetime == nil {
		return
	}
	b.lifetime.Lock()
	defer b.lifetime.Unlock()
	if b.lifetime.closed {
		return
	}
	b.lifetime.closed = true
	for _, pin := range b.lifetime.pins {
		pin.Close()
	}
}

func (b Bound) Check() error {
	if b.lifetime == nil {
		return errors.New("session binding is closed")
	}
	b.lifetime.RLock()
	defer b.lifetime.RUnlock()
	if b.lifetime.closed {
		return errors.New("session binding is closed")
	}
	for path, old := range map[string]os.FileInfo{b.Socket: b.apiInfo, b.clientPath: b.clientInfo} {
		now, e := os.Stat(path)
		if e != nil {
			return e
		}
		if !os.SameFile(old, now) || !old.ModTime().Equal(now.ModTime()) {
			return errors.New("session endpoint replaced; refresh publication")
		}
	}
	return nil
}
func (b Bound) Dial() (net.Conn, error) {
	if e := b.Check(); e != nil {
		return nil, e
	}
	return net.DialTimeout("unix", b.clientPath, 3*time.Second)
}
func (b Bound) Metadata(kind string) ([]byte, error) {
	if e := b.Check(); e != nil {
		return nil, e
	}
	if kind == "platform" {
		osName, arch := "Linux", "x86_64"
		if runtime.GOOS == "darwin" {
			osName = "Darwin"
		}
		if runtime.GOARCH == "arm64" {
			arch = "aarch64"
		}
		return []byte(osName + "\n" + arch + "\n"), nil
	}
	which := "server"
	if kind == "status-client" {
		which = "client"
	} else if kind != "status-server" {
		return nil, errors.New("unsupported metadata operation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return command(ctx, b.Socket, "status", which, "--json")
}
