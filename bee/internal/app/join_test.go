package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
	"golang.org/x/crypto/ssh"
)

func testInvitation(t *testing.T) (invitation, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	v := invitation{1, "example.test:2222", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))), "hreg-" + strings.Repeat("a", 64)}
	b, _ := json.Marshal(v)
	return v, invitationPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func TestInvitationRejectsMalformedAndInjection(t *testing.T) {
	v, raw := testInvitation(t)
	if got, err := parseInvitation(raw); err != nil || got != v {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"", "hreg-secret", raw + "!", strings.Repeat("x", maxInvitation+1)} {
		if _, err := parseInvitation(bad); err == nil {
			t.Fatal("accepted invalid invitation")
		}
	}
	for _, host := range []string{"host\nProxyCommand touch:22", "-oProxyCommand=bad:22", "*.test:22", "host:0", "[bad:ipv6]:22", "host:65536", "$(command):22"} {
		v.Hive = host
		b, _ := json.Marshal(v)
		if _, err := parseInvitation(invitationPrefix + base64.RawURLEncoding.EncodeToString(b)); err == nil {
			t.Fatalf("accepted %q", host)
		}
	}
}

func TestJoinResumesWithoutTokenAndPreservesIdentity(t *testing.T) {
	oldEnroll, oldDirectory, oldMachine := enrollDevice, joinDirectory, joinMachine
	t.Cleanup(func() { enrollDevice, joinDirectory, joinMachine = oldEnroll, oldDirectory, oldMachine })
	dir := t.TempDir()
	var out bytes.Buffer
	a := App{Dir: dir, Out: &out}
	_, raw := testInvitation(t)
	registered, enrollmentCalls := false, 0
	joinDirectory = func(context.Context, config.Config) ([]publisher.Share, error) {
		if !registered {
			return nil, errors.New("unregistered")
		}
		return []publisher.Share{}, nil
	}
	var identity []byte
	enrollDevice = func(_ context.Context, c config.Config, token string) (string, error) {
		enrollmentCalls++
		registered = true
		var err error
		identity, err = os.ReadFile(c.IdentityFile)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ssh.ParsePrivateKey(identity); err != nil {
			t.Fatal(err)
		}
		return "registered", nil
	}
	joinMachine = func(context.Context, string, config.Config) error { return errors.New("native setup interrupted") }
	if err := a.join(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "registered; finish") {
		t.Fatal(err)
	}
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || len(c.Rules) != 0 {
		t.Fatal("join enabled sharing")
	}
	for _, path := range []string{"config.json", "device_key"} {
		b, _ := os.ReadFile(filepath.Join(dir, path))
		if bytes.Contains(b, []byte(raw)) || bytes.Contains(b, []byte("hreg-")) {
			t.Fatal("persisted invitation/token")
		}
		fi, _ := os.Stat(filepath.Join(dir, path))
		if fi.Mode().Perm() != 0600 {
			t.Fatal("nonprivate file")
		}
	}
	joinMachine = func(context.Context, string, config.Config) error { return nil }
	if err = a.join(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err = a.join(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if enrollmentCalls != 1 {
		t.Fatal("re-enrolled existing device")
	}
	after, _ := os.ReadFile(c.IdentityFile)
	if !bytes.Equal(identity, after) {
		t.Fatal("identity rotated")
	}
}

func TestFailedJoinDoesNotChangeConnectionOrShareSelection(t *testing.T) {
	oldEnroll, oldDirectory := enrollDevice, joinDirectory
	t.Cleanup(func() { enrollDevice, joinDirectory = oldEnroll, oldDirectory })
	dir := t.TempDir()
	a := App{Dir: dir, Out: &bytes.Buffer{}}
	_, raw := testInvitation(t)
	joinDirectory = func(context.Context, config.Config) ([]publisher.Share, error) { return nil, errors.New("offline") }
	enrollDevice = func(context.Context, config.Config, string) (string, error) { return "", errors.New("rejected") }
	if err := a.join(context.Background(), raw); err == nil {
		t.Fatal("accepted rejection")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("saved failed enrollment")
	}
	_, err := config.Update(dir, func(c *config.Config) error { c.Hive = "previous.test:2222"; c.Enabled = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if err = a.join(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "stop sharing") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("changed old sharing")
	}
}

func TestJoinRefusesChangedTrustedHostKey(t *testing.T) {
	oldEnroll, oldDirectory, oldMachine := enrollDevice, joinDirectory, joinMachine
	t.Cleanup(func() { enrollDevice, joinDirectory, joinMachine = oldEnroll, oldDirectory, oldMachine })
	joinDirectory = func(context.Context, config.Config) ([]publisher.Share, error) { return []publisher.Share{}, nil }
	joinMachine = func(context.Context, string, config.Config) error { return nil }
	enrollDevice = func(context.Context, config.Config, string) (string, error) {
		t.Fatal("sent token despite trusted identity")
		return "", nil
	}
	dir := t.TempDir()
	a := App{Dir: dir, Out: &bytes.Buffer{}}
	_, raw := testInvitation(t)
	if err := a.join(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	_, different := testInvitation(t)
	if err := a.join(context.Background(), different); err == nil || !strings.Contains(err.Error(), "saved Hive host key") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("changed trusted config")
	}
}

func TestManagedSSHAndMachineAreIdempotent(t *testing.T) {
	oldHome, oldCommand := joinHome, machineCommand
	t.Cleanup(func() { joinHome, machineCommand = oldHome, oldCommand })
	root := t.TempDir()
	dir := filepath.Join(root, "bee data")
	os.MkdirAll(dir, 0700)
	joinHome = func() (string, error) { return root, nil }
	os.MkdirAll(filepath.Join(root, ".ssh"), 0700)
	original := []byte("# original\nHost *\n ServerAliveInterval 10\n")
	target := filepath.Join(root, "dotfile")
	os.WriteFile(target, original, 0600)
	os.Symlink(target, filepath.Join(root, ".ssh/config"))
	c := config.Config{Hive: "[::1]:2222", IdentityFile: filepath.Join(dir, "key"), KnownHosts: filepath.Join(dir, "known")}
	added := false
	adds := 0
	machineCommand = func(_ context.Context, args ...string) ([]byte, error) {
		switch args[1] {
		case "list":
			if added {
				b, _ := json.Marshal([]map[string]any{{"id": "native-id", "target": machineTarget(c), "enabled": true}})
				return b, nil
			}
			return []byte("[]"), nil
		case "add":
			added = true
			adds++
			return nil, nil
		default:
			t.Fatal(args)
			return nil, nil
		}
	}
	for i := 0; i < 2; i++ {
		if err := ensureMachine(context.Background(), dir, c); err != nil {
			t.Fatal(err)
		}
	}
	if adds != 1 || !connectionComplete(dir, c) {
		t.Fatal("duplicate or incomplete machine")
	}
	b, _ := os.ReadFile(target)
	if !bytes.HasSuffix(b, original) || bytes.Count(b, []byte("Include ")) != 1 {
		t.Fatal("modified original SSH settings")
	}
	fi, _ := os.Lstat(filepath.Join(root, ".ssh/config"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced symlink")
	}
	for _, p := range []string{"relative", "/tmp/$(id)", "/tmp/%h", "/tmp/*", "/tmp/line\nfeed"} {
		if _, err := sshPath(p); err == nil {
			t.Fatalf("accepted path %q", p)
		}
	}
}

func TestJoinPanelMasksInvitationAndSubmitsInProcess(t *testing.T) {
	m := newPanel(context.Background(), App{})
	m.ready = true
	m.snapshot.config = config.Config{Name: "Device"}
	m.syncFields()
	m.focus = 0
	_, raw := testInvitation(t)
	next, _ := m.Update(tea.PasteMsg{Content: raw})
	m = next.(panelModel)
	if !m.editing || m.fields[4].Value() != raw || strings.Contains(m.View().Content, raw[6:30]) {
		t.Fatal("invitation lost or exposed")
	}
	var commands [][]string
	m.execute = func(args [][]string) tea.Cmd { commands = args; return nil }
	m.editing = false
	m.focus = 2
	m.activate()
	if len(commands) != 1 || commands[0][0] != "join" || commands[0][1] != raw {
		t.Fatal("incorrect join command")
	}
	m.read = func(int) tea.Cmd { return nil }
	next, _ = m.Update(panelDone{saved: true, joined: true})
	m = next.(panelModel)
	if m.fields[4].Value() != "" || m.dirty {
		t.Fatal("invitation retained")
	}
}

func TestLegacyMachineNeedsExactSSHConnection(t *testing.T) {
	oldHome, oldResolve := joinHome, resolveSSH
	t.Cleanup(func() { joinHome, resolveSSH = oldHome, oldResolve })
	root := t.TempDir()
	joinHome = func() (string, error) { return root, nil }
	c := config.Config{Hive: "host.test:2222", IdentityFile: filepath.Join(root, "key"), KnownHosts: filepath.Join(root, "known")}
	valid := "hostname host.test\nport 2222\nuser hive\nstricthostkeychecking true\nidentitiesonly yes\nidentityfile ~/key\nuserknownhostsfile ~/known\n"
	for _, change := range []struct{ from, to string }{{"", ""}, {"2222", "22"}, {"user hive", "user root"}, {"true", "false"}, {"~/key", "~/other"}, {"~/known", "~/unverified"}, {"identityfile ~/key", "identityfile ~/other\nidentityfile ~/key"}, {"identityfile ~/key", "identityfile ~/key\ncertificatefile ~/other-cert.pub"}} {
		text := valid
		if change.from != "" {
			text = strings.ReplaceAll(text, change.from, change.to)
		}
		resolveSSH = func(context.Context, string) ([]byte, error) { return []byte(text), nil }
		if sameSSH(context.Background(), "legacy", c) != (change.from == "") {
			t.Fatal("incorrect legacy connection match", change.from)
		}
	}
}

func TestSwitchHivePreservesEarlierSSHConnections(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH not installed")
	}
	oldHome := joinHome
	t.Cleanup(func() { joinHome = oldHome })
	root := t.TempDir()
	joinHome = func() (string, error) { return root, nil }
	dir := filepath.Join(root, "bee")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a := config.Config{Hive: "192.0.2.1:2222", IdentityFile: filepath.Join(dir, "key"), KnownHosts: filepath.Join(dir, "known-a")}
	b := a
	b.Hive = "192.0.2.2:3333"
	b.KnownHosts = filepath.Join(dir, "known-b")
	for _, next := range []config.Config{a, b, a} {
		if err := installSSH(dir, next); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []config.Config{a, b} {
		out, err := exec.Command("ssh", "-F", filepath.Join(root, ".ssh/config"), "-G", machineTarget(c)).Output()
		if err != nil {
			t.Fatal(err)
		}
		host, port, _ := hiveAddress(c.Hive)
		if !bytes.Contains(out, []byte("hostname "+host+"\n")) || !bytes.Contains(out, []byte("port "+port+"\n")) {
			t.Fatal("switch lost earlier SSH target")
		}
	}
}
