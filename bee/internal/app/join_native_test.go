package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

// native.py owns the disposable Hive, OpenSSH wrapper and isolated Herdr profile.
// Only the home-directory lookup is redirected; registration and machine setup
// execute against the real Hive, OpenSSH and installed Herdr binary.
func TestNativeInvitationJoin(t *testing.T) {
	root := os.Getenv("BEE_TEST_JOIN_ROOT")
	if root == "" {
		t.Skip("run tests/integration/native.py")
	}
	oldHome, oldMachine := joinHome, joinMachine
	t.Cleanup(func() { joinHome, joinMachine = oldHome, oldMachine })
	joinHome = func() (string, error) { return filepath.Join(root, "join-home"), nil }
	dir := filepath.Join(root, "join-bee")
	raw, err := os.ReadFile(filepath.Join(root, "invitation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		ID         string `json:"id"`
		Invitation string `json:"invitation"`
	}
	if err = json.Unmarshal(raw, &issued); err != nil {
		t.Fatal(err)
	}
	a := App{Dir: dir, Out: &bytes.Buffer{}}
	// Inject a native-setup interruption after successful real registration.
	joinMachine = func(context.Context, string, config.Config) error { return errors.New("test setup interruption") }
	if err = a.join(context.Background(), issued.Invitation); err == nil || !strings.Contains(err.Error(), "registered; finish") {
		t.Fatal(err)
	}
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(c.IdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	// Revoking the invitation must not prevent the registered device from resuming.
	cmd := exec.Command(filepath.Join(root, "install/hive"), "enroll", "revoke", issued.ID, "--tokens", filepath.Join(root, "authorized_tokens"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("revoke: %v %s", err, out)
	}
	joinMachine = oldMachine
	if err = a.join(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err = a.join(context.Background(), issued.Invitation); err != nil {
		t.Fatal(err)
	}
	data, err := machineCommand(context.Background(), "machine", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []struct {
		ID     string `json:"id"`
		Target string `json:"target"`
	}
	if err = json.Unmarshal(data, &profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Target != machineTarget(c) {
		t.Fatal("missing or duplicate joined machine")
	}
	if !connectionComplete(dir, c) || c.Enabled || len(c.Rules) != 0 {
		t.Fatal("join incomplete or implicitly sharing")
	}
	after, _ := os.ReadFile(c.IdentityFile)
	if !bytes.Equal(key, after) {
		t.Fatal("device key changed")
	}
	if _, err = machineCommand(context.Background(), "machine", "disable", profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = a.join(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	// Check the actual OpenSSH resolved endpoint, including quoted paths.
	resolved, err := exec.Command("ssh", "-G", machineTarget(c)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(resolved, []byte("user hive\n")) || !bytes.Contains(resolved, []byte("stricthostkeychecking true\n")) {
		t.Fatal("wrong SSH configuration")
	}
	if out, err := exec.Command("ssh", machineTarget(c), "list --json").Output(); err != nil || strings.TrimSpace(string(out)) != "[]" {
		t.Fatal("empty Hive is not reachable", err)
	}
	if _, err = machineCommand(context.Background(), "machine", "remove", profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	// A manually added equivalent Hive is retained rather than duplicated.
	managed, err := os.ReadFile(filepath.Join(dir, "ssh-connections", machineTarget(c)+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	sshConfig := filepath.Join(root, "join-home/.ssh/config")
	original, err := os.ReadFile(sshConfig)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.ReplaceAll(managed, []byte(machineTarget(c)), []byte("legacy-hive"))
	if err = os.WriteFile(sshConfig, append(legacy, original...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = machineCommand(context.Background(), "machine", "add", "legacy-hive", "--label", "Hive"); err != nil {
		t.Fatal(err)
	}
	if err = a.join(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	data, err = machineCommand(context.Background(), "machine", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(data, &profiles)
	record, ok := connectionRecord(dir, c)
	if !ok || record.Target != "legacy-hive" || len(profiles) != 1 || profiles[0].Target != "legacy-hive" {
		t.Fatal("duplicated legacy Hive")
	}
	if _, err = machineCommand(context.Background(), "machine", "remove", profiles[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInvitationPanel(t *testing.T) {
	root := os.Getenv("BEE_TEST_JOIN_ROOT")
	if root == "" {
		t.Skip("run tests/integration/native.py")
	}
	oldHome := joinHome
	t.Cleanup(func() { joinHome = oldHome })
	joinHome = func() (string, error) { return filepath.Join(root, "join-home"), nil }
	a := App{Dir: filepath.Join(root, "join-ui-bee"), Out: os.Stdout, Executable: filepath.Join(root, "install/bee"), Version: "test"}
	if err := a.TUI(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hive == "" || !connectionComplete(a.Dir, c) || c.Enabled || len(c.Rules) != 0 {
		t.Fatal("panel did not complete joining")
	}
	data, err := machineCommand(context.Background(), "machine", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []struct{ ID, Target string }
	if err = json.Unmarshal(data, &profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Target != machineTarget(c) {
		t.Fatal("panel created incorrect machine")
	}
	if _, err = machineCommand(context.Background(), "machine", "remove", profiles[0].ID); err != nil {
		t.Fatal(err)
	}
}
