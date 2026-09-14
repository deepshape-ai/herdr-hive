package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

func machineTarget(c config.Config) string {
	return fmt.Sprintf("bee-hive-%x", sha256.Sum256([]byte(c.Hive+"\x00"+c.IdentityFile)))[:25]
}

type joinedConnection struct {
	Hive       string `json:"hive"`
	Identity   string `json:"identity"`
	KnownHosts string `json:"known_hosts"`
	Target     string `json:"target"`
}

func joined(c config.Config) joinedConnection {
	return joinedConnection{c.Hive, c.IdentityFile, c.KnownHosts, machineTarget(c)}
}
func connectionComplete(dir string, c config.Config) bool {
	_, ok := connectionRecord(dir, c)
	return ok
}
func connectionRecord(dir string, c config.Config) (joinedConnection, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "joined.json"))
	var record joinedConnection
	ok := err == nil && json.Unmarshal(b, &record) == nil && record.Hive == c.Hive && record.Identity == c.IdentityFile && record.KnownHosts == c.KnownHosts && record.Target != ""
	return record, ok
}

// OpenSSH expands percent tokens even inside quotes. Reject expansion and glob
// characters instead of writing a path whose meaning differs across directives.
func sshPath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00\"\\%$*?[]") {
		return "", errors.New("SSH configuration paths must be absolute and contain no expansion characters")
	}
	return `"` + path + `"`, nil
}

func installSSH(dir string, c config.Config) error {
	host, port, err := hiveAddress(c.Hive)
	if err != nil {
		return err
	}
	identity, err := sshPath(c.IdentityFile)
	if err != nil {
		return err
	}
	known, err := sshPath(c.KnownHosts)
	if err != nil {
		return err
	}
	managedPath := filepath.Join(dir, "ssh_config")
	managed, err := sshPath(managedPath)
	if err != nil {
		return err
	}
	home, err := joinHome()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err = os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(sshDir, ".bee-config.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("SSH configuration is being updated; retry connection")
	}
	path := filepath.Join(sshDir, "config")
	// Preserve symlinked dotfiles: update their target, never replace the symlink.
	if fi, e := os.Lstat(path); e == nil && fi.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("SSH config exceeds 1 MiB")
	}
	content := fmt.Sprintf("# Managed by Bee.\nHost %s\n HostName %s\n Port %s\n User hive\n IdentityFile %s\n UserKnownHostsFile %s\n IdentitiesOnly yes\n StrictHostKeyChecking yes\n BatchMode yes\n ControlMaster no\n ControlPath none\n ProxyCommand none\n ProxyJump none\n ForwardAgent no\n PermitLocalCommand no\nHost *\n", machineTarget(c), host, port, identity, known)
	// Keep prior targets resolvable when switching Hive or device identity. One
	// atomic file per target also preserves working connections if new setup fails.
	connections := filepath.Join(dir, "ssh-connections")
	if err = os.MkdirAll(connections, 0700); err != nil {
		return err
	}
	if err = privateWrite(filepath.Join(connections, machineTarget(c)+".conf"), []byte(content)); err != nil {
		return err
	}
	// The only glob is constructed here; the containing path was validated above.
	index := "Include \"" + filepath.Join(connections, "*.conf") + "\"\nHost *\n"
	if err = privateWrite(managedPath, []byte(index)); err != nil {
		return err
	}
	include := "Include " + managed + "\n"
	// Put our include before Host/Match blocks; an identical line nested later is
	// not sufficient. Preserve every byte of the original configuration below it.
	if !bytes.HasPrefix(b, []byte(include)) {
		// Other Bee installations can prepend their own include between joins.
		// Move only our exact directive back to the top instead of duplicating it.
		var kept bytes.Buffer
		for _, line := range bytes.SplitAfter(b, []byte("\n")) {
			if !bytes.Equal(line, []byte(include)) {
				kept.Write(line)
			}
		}
		b = kept.Bytes()
		if err = privateWrite(path, append([]byte(include), b...)); err != nil {
			return err
		}
	}
	return nil
}

type commandOutput struct{ bytes.Buffer }

func (b *commandOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("command output exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}

var machineCommand = func(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", args...)
	// Never permit an unattended install/restart prompt or mix output into the UI.
	cmd.Stdin = nil
	var out, stderr commandOutput
	cmd.Stdout, cmd.Stderr = &out, &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("herdr %s failed: %s (%w)", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return out.Bytes(), nil
}

var resolveSSH = func(ctx context.Context, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-G", "--", target)
	var out commandOutput
	cmd.Stdout = &out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Reuse a manually added Hive only when it resolves to this exact connection.
// A matching display label alone never identifies a device or a trusted server.
func sameSSH(ctx context.Context, target string, c config.Config) bool {
	b, err := resolveSSH(ctx, target)
	if err != nil {
		return false
	}
	fields := map[string][]string{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok {
			fields[key] = append(fields[key], strings.TrimSpace(value))
		}
	}
	one := func(key string) string {
		if len(fields[key]) == 1 {
			return fields[key][0]
		}
		return ""
	}
	host, port, err := hiveAddress(c.Hive)
	if err != nil || !strings.EqualFold(one("hostname"), host) || one("port") != port || one("user") != "hive" || one("stricthostkeychecking") != "true" || one("identitiesonly") != "yes" {
		return false
	}
	home, err := joinHome()
	if err != nil {
		return false
	}
	expand := func(s string) string {
		if strings.HasPrefix(s, "~/") {
			return filepath.Join(home, s[2:])
		}
		return s
	}
	if expand(one("userknownhostsfile")) != c.KnownHosts {
		return false
	}
	if len(fields["certificatefile"]) > 0 && one("certificatefile") != "none" {
		return false
	}
	return len(fields["identityfile"]) == 1 && expand(one("identityfile")) == c.IdentityFile
}

func ensureMachine(ctx context.Context, dir string, c config.Config) error {
	if err := installSSH(dir, c); err != nil {
		return err
	}
	data, err := machineCommand(ctx, "machine", "list", "--json")
	if err != nil {
		return err
	}
	var profiles []struct {
		ID      string `json:"id"`
		Target  string `json:"target"`
		Enabled bool   `json:"enabled"`
		Session string `json:"session"`
	}
	if err = json.Unmarshal(data, &profiles); err != nil {
		return errors.New("invalid Herdr machine list")
	}
	target := machineTarget(c)
	found := false
	// Prefer the managed target, then inspect at most 16 existing profiles so a
	// large unrelated machine list cannot make joining unbounded.
	for i, p := range profiles {
		if p.Target == target {
			profiles[0], profiles[i] = profiles[i], profiles[0]
			break
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	for i, p := range profiles {
		if p.Session != "" && p.Session != "default" {
			continue
		}
		if p.Target == target || i < 16 && sameSSH(ctx, p.Target, c) {
			found = true
			target = p.Target
			if !p.Enabled {
				if _, err = machineCommand(ctx, "machine", "enable", p.ID); err != nil {
					return err
				}
			}
			break
		}
	}
	if !found {
		if _, err = machineCommand(ctx, "machine", "add", target, "--label", "Hive"); err != nil {
			return err
		}
	}
	record := joined(c)
	record.Target = target
	b, _ := json.Marshal(record)
	return privateWrite(filepath.Join(dir, "joined.json"), b)
}
