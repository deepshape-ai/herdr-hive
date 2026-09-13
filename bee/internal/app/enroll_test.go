package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

func TestConfigureEnrollmentTransaction(t *testing.T) {
	dir := t.TempDir()
	identity := filepath.Join(dir, "key")
	known := filepath.Join(dir, "known")
	os.WriteFile(identity, nil, 0600)
	os.WriteFile(known, nil, 0600)
	config.Update(dir, func(c *config.Config) error { c.Hive = "old:2222"; c.KnownHosts = known; return nil })
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	a := App{Dir: dir, Out: io.Discard}
	original := enrollDevice
	defer func() { enrollDevice = original }()
	token := "hreg-" + strings.Repeat("a", 64)
	calls := 0
	enrollDevice = func(ctx context.Context, c config.Config, s string) (string, error) {
		calls++
		if c.Hive != "new:2222" || c.IdentityFile != identity || c.KnownHosts != known || s != token {
			t.Fatal("wrong enrollment config")
		}
		return "", errors.New("rejected")
	}
	args := []string{"configure", "--hive", "new:2222", "--identity", identity, "--token", token}
	if e := a.Execute(context.Background(), args); e == nil || !strings.Contains(e.Error(), "no settings were changed") {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) || calls != 1 {
		t.Fatal("failed registration changed config")
	}
	enrollDevice = func(ctx context.Context, c config.Config, s string) (string, error) {
		if c.KnownHosts != known {
			t.Fatal("trust path changed")
		}
		return "SHA256:test", nil
	}
	if e := a.Execute(context.Background(), args); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(filepath.Join(dir, "config.json"))
	if bytes.Contains(after, []byte(token)) {
		t.Fatal("saved token")
	}
	c, e := config.Load(dir)
	if e != nil || c.Hive != "new:2222" || c.KnownHosts != known {
		t.Fatal(c, e)
	}
}
