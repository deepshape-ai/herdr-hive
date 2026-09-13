package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
)

func TestConfigureKeepsCustomKnownHostsWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	identity := filepath.Join(dir, "identity")
	trusted := filepath.Join(dir, "custom-known-hosts")
	replacement := filepath.Join(dir, "other-known-hosts")
	for _, path := range []string{identity, trusted, replacement} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := config.Update(dir, func(c *config.Config) error { c.KnownHosts = trusted; return nil }); err != nil {
		t.Fatal(err)
	}
	a := App{Dir: dir, Out: io.Discard}
	args := []string{"configure", "--hive", "hive.example.internal:2222", "--identity", identity}
	if err := a.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir)
	if err != nil || c.KnownHosts != trusted {
		t.Fatalf("existing trust store lost: %+v %v", c, err)
	}
	if err = a.Execute(context.Background(), append(args, "--known-hosts", replacement)); err != nil {
		t.Fatal(err)
	}
	c, err = config.Load(dir)
	if err != nil || c.KnownHosts != replacement {
		t.Fatalf("explicit override ignored: %+v %v", c, err)
	}
}
func TestDefaultKnownHostsIsStandardSSHPath(t *testing.T) {
	path, err := knownHostsPath("", "")
	home, homeErr := os.UserHomeDir()
	if err != nil || homeErr != nil || path != filepath.Join(home, ".ssh", "known_hosts") {
		t.Fatalf("unexpected trust path %q %v", path, err)
	}
}
