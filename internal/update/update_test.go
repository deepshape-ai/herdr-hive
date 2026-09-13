package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func archive(t *testing.T, component string, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	w := tar.NewWriter(gz)
	for name, data := range entries {
		if e := w.WriteHeader(&tar.Header{Name: component + "-" + runtime.GOOS + "-" + runtime.GOARCH + "/" + name, Mode: 0755, Size: int64(len(data))}); e != nil {
			t.Fatal(e)
		}
		w.Write([]byte(data))
	}
	w.Close()
	gz.Close()
	return b.Bytes()
}
func setup(t *testing.T, corrupt bool) (*Installer, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "bee")
	os.WriteFile(exe, []byte("#!/bin/sh\necho 0.0.1\n"), 0755)
	os.WriteFile(filepath.Join(dir, "herdr-plugin.toml"), []byte("id = \"herdr.bee\"\nversion = \"0.0.1\"\n"), 0644)
	blob := archive(t, "bee", map[string]string{"bee": "#!/bin/sh\necho 0.1.0\n", "herdr-plugin.toml": "id = \"herdr.bee\"\nversion = \"0.1.0\"\n"})
	sum := fmt.Sprintf("%x", sha256.Sum256(blob))
	if corrupt {
		sum = strings.Repeat("0", 64)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			json.NewEncoder(w).Encode([]any{
				map[string]any{"tag_name": "hive/v9.0.0"}, map[string]any{"tag_name": "bee/v4.0.0", "draft": true}, map[string]any{"tag_name": "bee/v3.0.0", "prerelease": true}, map[string]any{"tag_name": "bee/v0.1.0"}, map[string]any{"tag_name": "bee/v0.0.2"},
			})
		case strings.HasSuffix(r.URL.Path, "SHA256SUMS"):
			fmt.Fprintf(w, "%s  bee-0.1.0-%s-%s.tar.gz\n", sum, runtime.GOOS, runtime.GOARCH)
		default:
			w.Write(blob)
		}
	}))
	i, e := open("bee", exe)
	if e != nil {
		t.Fatal(e)
	}
	i.api = srv.URL + "/releases"
	i.downloads = srv.URL + "/downloads/"
	i.client = srv.Client()
	t.Cleanup(func() { i.Close(); srv.Close() })
	return i, srv
}
func TestVerifiedUpgradeAndIdempotency(t *testing.T) {
	i, _ := setup(t, false)
	result, e := i.Install(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !result.Updated || result.Version != "0.1.0" {
		t.Fatalf("%+v", result)
	}
	b, e := os.ReadFile(filepath.Join(filepath.Dir(i.Executable), "herdr-plugin.toml"))
	if e != nil || !bytes.Contains(b, []byte(`version = "0.1.0"`)) {
		t.Fatalf("manifest: %s %v", b, e)
	}
	next, e := open("bee", i.Executable)
	if e == nil {
		next.Close()
		t.Fatal("concurrent updater acquired lock")
	}
	// A new command instance sees the installed release and performs no replacement.
	i.Updated = false
	result, e = i.Install(context.Background())
	if e != nil || result.Updated {
		t.Fatalf("idempotent update: %+v %v", result, e)
	}
}
func TestChecksumFailurePreservesInstallation(t *testing.T) {
	i, _ := setup(t, true)
	old, _ := os.ReadFile(i.Executable)
	manifest := filepath.Join(filepath.Dir(i.Executable), "herdr-plugin.toml")
	oldManifest, _ := os.ReadFile(manifest)
	if _, e := i.Install(context.Background()); e == nil || !strings.Contains(e.Error(), "checksum mismatch") {
		t.Fatalf("%v", e)
	}
	now, _ := os.ReadFile(i.Executable)
	newManifest, _ := os.ReadFile(manifest)
	if !bytes.Equal(old, now) || !bytes.Equal(oldManifest, newManifest) {
		t.Fatal("failed download changed installation")
	}
}
func TestRejectArchiveTraversalAndLinks(t *testing.T) {
	b := archive(t, "hive", map[string]string{"hive": "ok", "../outside": "bad"})
	if _, e := unpack(b, "hive"); e == nil {
		t.Fatal("accepted traversal")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	w.WriteHeader(&tar.Header{Name: "hive-" + runtime.GOOS + "-" + runtime.GOARCH + "/hive", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	w.Close()
	gz.Close()
	if _, e := unpack(buf.Bytes(), "hive"); e == nil {
		t.Fatal("accepted symlink")
	}
}
func TestCancelledDownloadDoesNotChangeBinary(t *testing.T) {
	i, srv := setup(t, false)
	i.api = srv.URL + "/releases"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, e := i.Install(ctx); e == nil {
		t.Fatal("cancelled update succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation blocked")
	}
	v, e := Version(context.Background(), i.Executable)
	if e != nil || v != "0.0.1" {
		t.Fatalf("%s %v", v, e)
	}
}
func TestNumericVersionOrdering(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{{"0.10.0", "0.9.9", true}, {"1.0.0", "2.0.0", false}, {"0.1.0", "0.1.0", false}, {"0.1.0", "dev", true}, {"0.1.0", "0.2.0-rc.1", false}, {"0.2.0", "0.2.0-rc.1", true}} {
		if newer(tt.a, tt.b) != tt.want {
			t.Fatalf("%+v", tt)
		}
	}
}

func TestVersionOutputIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noisy")
	if e := os.WriteFile(path, []byte("#!/bin/sh\nhead -c 8192 /dev/zero\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Version(context.Background(), path); e == nil {
		t.Fatal("accepted excessive version output")
	}
}
