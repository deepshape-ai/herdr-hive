// Package update installs stable component releases without changing configuration.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const repository = "deepshape-ai/herdr-hive"
const maxArchive = 64 << 20

var stable = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Result struct {
	Component  string `json:"component"`
	Version    string `json:"version"`
	Updated    bool   `json:"updated"`
	Executable string `json:"executable"`
}

// Installer holds a per-installation lock through process activation.
type Installer struct {
	Result
	client         *http.Client
	api, downloads string
	lock           *os.File
}

func Open(component string) (*Installer, error) {
	if component != "bee" && component != "hive" {
		return nil, errors.New("unknown component")
	}
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return nil, e
	}
	return open(component, exe)
}
func open(component, exe string) (*Installer, error) {
	lock, e := os.OpenFile(filepath.Join(filepath.Dir(exe), "."+component+"-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, fmt.Errorf("installation directory must be writable: %w", e)
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("another update is in progress")
	}
	return &Installer{Result: Result{Component: component, Executable: exe}, lock: lock,
		api: "https://api.github.com/repos/" + repository + "/releases", downloads: "https://github.com/" + repository + "/releases/download/",
		client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			switch req.URL.Hostname() {
			case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			default:
				return errors.New("unexpected download redirect")
			}
			if req.URL.Scheme != "https" {
				return errors.New("HTTPS required")
			}
			return nil
		}}}, nil
}
func (i *Installer) Close() { i.lock.Close() }
func (i *Installer) get(ctx context.Context, address string, max int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "herdr-"+i.Component+"-updater")
	resp, e := i.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("release download: HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, errors.New("release response exceeds size limit")
	}
	return b, nil
}
func newer(a, b string) bool {
	core, prerelease, _ := strings.Cut(b, "-")
	if !stable.MatchString(core) {
		return true
	}
	aa, bb := strings.Split(a, "."), strings.Split(core, ".")
	for j := range 3 {
		if len(aa[j]) != len(bb[j]) {
			return len(aa[j]) > len(bb[j])
		}
		if aa[j] != bb[j] {
			return aa[j] > bb[j]
		}
	}
	return prerelease != ""
}
func (i *Installer) latest(ctx context.Context) (string, error) {
	best := ""
	for page := 1; page <= 10; page++ {
		b, e := i.get(ctx, i.api+"?per_page=100&page="+strconv.Itoa(page), 4<<20)
		if e != nil {
			return "", e
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if e = json.Unmarshal(b, &releases); e != nil {
			return "", e
		}
		for _, r := range releases {
			v, ok := strings.CutPrefix(r.Tag, i.Component+"/v")
			if ok && !r.Draft && !r.Prerelease && stable.MatchString(v) && newer(v, best) {
				best = v
			}
		}
		if len(releases) < 100 {
			if best == "" {
				return "", errors.New("no stable release available for " + i.Component)
			}
			return best, nil
		}
	}
	return "", errors.New("release catalog exceeds 1000 entries; update discovery needs a newer client")
}
func Version(ctx context.Context, exe string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version")
	var out limitedOutput
	cmd.Stdout = &out
	cmd.Stderr = &limitedOutput{}
	cmd.WaitDelay = time.Second
	e := cmd.Run()
	b := out.buf.Bytes()
	if e != nil {
		return "", fmt.Errorf("binary version check: %w", e)
	}
	return strings.TrimSpace(string(b)), nil
}

type limitedOutput struct{ buf bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > 4096 {
		return 0, errors.New("version output exceeds limit")
	}
	return b.buf.Write(p)
}

func (i *Installer) Install(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	current, e := Version(ctx, i.Executable)
	if e != nil {
		return i.Result, e
	}
	v, e := i.latest(ctx)
	if e != nil {
		return i.Result, e
	}
	i.Version = current
	if !newer(v, current) {
		return i.Result, nil
	}
	name := fmt.Sprintf("%s-%s-%s-%s.tar.gz", i.Component, v, runtime.GOOS, runtime.GOARCH)
	base := i.downloads + url.PathEscape(i.Component+"/v"+v) + "/"
	sums, e := i.get(ctx, base+"SHA256SUMS", 64<<10)
	if e != nil {
		return i.Result, e
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if want != "" {
				return i.Result, errors.New("duplicate checksum")
			}
			want = fields[0]
		}
	}
	if decoded, e := hex.DecodeString(want); e != nil || len(decoded) != sha256.Size {
		return i.Result, errors.New("missing or invalid archive checksum")
	}
	archive, e := i.get(ctx, base+name, maxArchive)
	if e != nil {
		return i.Result, e
	}
	sum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
		return i.Result, errors.New("archive checksum mismatch")
	}
	files, e := unpack(archive, i.Component)
	if e != nil {
		return i.Result, e
	}
	binary, e := stage(filepath.Dir(i.Executable), files[i.Component], 0755)
	if e != nil {
		return i.Result, e
	}
	defer os.Remove(binary)
	got, e := Version(ctx, binary)
	if e != nil {
		return i.Result, e
	}
	if got != v {
		return i.Result, errors.New("binary version does not match release tag")
	}
	// Stage both files before replacing either. Configuration is outside the install directory.
	manifest := ""
	target := filepath.Join(filepath.Dir(i.Executable), "herdr-plugin.toml")
	if i.Component == "bee" {
		old, err := os.ReadFile(target)
		if err == nil {
			if !strings.Contains(string(old), `id = "herdr.bee"`) {
				return i.Result, errors.New("installation contains a different plugin manifest")
			}
			if !strings.Contains(string(files["herdr-plugin.toml"]), `version = "`+v+`"`) {
				return i.Result, errors.New("plugin version does not match release tag")
			}
			manifest, e = stage(filepath.Dir(target), files["herdr-plugin.toml"], 0644)
			if e != nil {
				return i.Result, e
			}
			defer os.Remove(manifest)
		} else if !os.IsNotExist(err) {
			return i.Result, err
		}
	}
	if manifest != "" {
		if e = os.Rename(manifest, target); e != nil {
			return i.Result, e
		}
	}
	if e = os.Rename(binary, i.Executable); e != nil {
		return i.Result, e
	}
	i.Version = v
	i.Updated = true
	return i.Result, nil
}
func stage(dir string, b []byte, mode os.FileMode) (string, error) {
	f, e := os.CreateTemp(dir, ".update-*")
	if e != nil {
		return "", e
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	if _, e = f.Write(b); e != nil {
		return "", e
	}
	if e = f.Chmod(mode); e != nil {
		return "", e
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	ok = true
	return name, nil
}
func unpack(b []byte, component string) (map[string][]byte, error) {
	z, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	defer z.Close()
	tr := tar.NewReader(io.LimitReader(z, 128<<20))
	prefix := component + "-" + runtime.GOOS + "-" + runtime.GOARCH + "/"
	files := map[string][]byte{}
	for count := 0; ; count++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if count >= 16 {
			return nil, errors.New("too many archive entries")
		}
		if h.Typeflag == tar.TypeDir && h.Name == prefix {
			continue
		}
		name, ok := strings.CutPrefix(h.Name, prefix)
		if !ok || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxArchive {
			return nil, errors.New("invalid release archive entry")
		}
		switch name {
		case component, "README.md", "LICENSE":
		case "herdr-plugin.toml":
			if component != "bee" {
				return nil, errors.New("unexpected manifest")
			}
		default:
			return nil, errors.New("unexpected release archive path")
		}
		if _, exists := files[name]; exists {
			return nil, errors.New("duplicate archive entry")
		}
		contents, e := io.ReadAll(io.LimitReader(tr, maxArchive+1))
		if e != nil {
			return nil, e
		}
		files[name] = contents
	}
	if len(files[component]) == 0 {
		return nil, errors.New("archive is missing binary")
	}
	if component == "bee" && !strings.Contains(string(files["herdr-plugin.toml"]), `id = "herdr.bee"`) {
		return nil, errors.New("archive is missing Bee manifest")
	}
	return files, nil
}
