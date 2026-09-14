package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const invitationPrefix = "hinv1-"
const maxInvitation = 16384

// This envelope is an offline convenience format, not a new enrollment protocol.
// Trust comes from the administrator's delivery channel and the pinned host key.
type invitation struct {
	Version int    `json:"version"`
	Hive    string `json:"hive"`
	HostKey string `json:"host_key"`
	Token   string `json:"token"`
}

func parseInvitation(raw string) (invitation, error) {
	var v invitation
	if len(raw) > maxInvitation {
		return v, errors.New("Hive invitation exceeds 16 KiB")
	}
	raw = strings.TrimSpace(raw)
	if len(raw) > maxInvitation || !strings.HasPrefix(raw, invitationPrefix) {
		return v, errors.New("paste a complete Hive invitation starting with hinv1-")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(raw, invitationPrefix))
	if err != nil {
		return v, errors.New("invalid Hive invitation encoding")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || v.Version != 1 {
		return invitation{}, errors.New("unsupported or invalid Hive invitation")
	}
	if _, _, err := hiveAddress(v.Hive); err != nil {
		return invitation{}, err
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(v.HostKey))
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || key == nil {
		return invitation{}, errors.New("invitation has an invalid Hive host key")
	}
	v.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	token, err := hex.DecodeString(strings.TrimPrefix(v.Token, "hreg-"))
	if err != nil || !strings.HasPrefix(v.Token, "hreg-") || len(token) != 32 || v.Token != strings.ToLower(v.Token) {
		return invitation{}, errors.New("invitation has an invalid enrollment token")
	}
	return v, nil
}

func hiveAddress(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || n < 1 || n > 65535 || len(host) > 253 {
		return "", "", errors.New("Hive address must be HOST:PORT")
	}
	// Only literal IPs and DNS names, never SSH options, expansions or patterns.
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-:", c)) {
			return "", "", errors.New("invalid Hive hostname")
		}
	}
	if strings.HasPrefix(host, "-") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", "", errors.New("invalid Hive hostname")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return "", "", errors.New("use a Hive address reachable by members, not a wildcard listen address")
	}
	return host, strconv.Itoa(n), nil
}

var joinDirectory = publisher.Directory
var joinMachine = ensureMachine
var joinHome = os.UserHomeDir

func privateWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".bee-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

func deviceKey(path string) error {
	if b, err := os.ReadFile(path); err == nil {
		_, err = ssh.ParsePrivateKey(b)
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(key, "Herdr Bee device")
	if err != nil {
		return err
	}
	return privateWrite(path, pem.EncodeToMemory(block))
}

// join is serialized across CLI and panel calls. Persist the device identity even
// on failure: a server may have accepted enrollment before the reply was lost.
func (a App) join(ctx context.Context, raw string) error {
	dir, err := filepath.Abs(a.Dir)
	if err != nil {
		return err
	}
	a.Dir = dir
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "join.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errors.New("another join is running; try again when it finishes")
	}
	current, err := config.Load(dir)
	if err != nil {
		return err
	}
	candidate := current
	if raw != "" {
		v, err := parseInvitation(raw)
		if err != nil {
			return err
		}
		if current.Hive != "" && current.Hive != v.Hive && (current.Enabled || len(current.Rules) != 0) {
			return errors.New("stop sharing and unselect sessions before joining a different Hive")
		}
		candidate.Hive = v.Hive
		if candidate.IdentityFile == "" {
			candidate.IdentityFile = filepath.Join(dir, "device_key")
			if err = deviceKey(candidate.IdentityFile); err != nil {
				return err
			}
		}
		key, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(v.HostKey))
		// Failed invitations leave only inert trust material. A corrected invitation
		// can retry without being trapped by an unverified earlier host key.
		name := fmt.Sprintf("hive-%x.known_hosts", sha256.Sum256([]byte(v.Hive+"\x00"+v.HostKey)))
		candidate.KnownHosts = filepath.Join(dir, name)
		if current.Hive == v.Hive && current.KnownHosts != "" {
			verify, e := knownhosts.New(current.KnownHosts)
			_, port, _ := hiveAddress(v.Hive)
			n, _ := strconv.Atoi(port)
			if e != nil || verify(v.Hive, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: n}, key) != nil {
				return errors.New("invitation differs from the saved Hive host key; verify the change with your administrator")
			}
			candidate.KnownHosts = current.KnownHosts
		}
		entry := []byte(knownhosts.Line([]string{knownhosts.Normalize(v.Hive)}, key) + "\n")
		if prior, e := os.ReadFile(candidate.KnownHosts); e == nil && candidate.KnownHosts != current.KnownHosts {
			if !bytes.Equal(prior, entry) {
				return errors.New("Hive host key changed; verify the change with your administrator before replacing the saved trust file")
			}
		} else if e != nil && !os.IsNotExist(e) {
			return e
		} else if os.IsNotExist(e) {
			if err = privateWrite(candidate.KnownHosts, entry); err != nil {
				return err
			}
		}
		// A previously enrolled device can resume even after its invitation expires.
		if _, err = joinDirectory(ctx, candidate); err != nil {
			if _, err = enrollDevice(ctx, candidate, v.Token); err != nil {
				return errors.New("could not join Hive; connection settings were not changed: " + err.Error())
			}
		}
		_, err = config.Update(dir, func(c *config.Config) error {
			if c.Hive != current.Hive || c.IdentityFile != current.IdentityFile || c.KnownHosts != current.KnownHosts || c.Enabled != current.Enabled || !sameRules(c.Rules, current.Rules) {
				return errors.New("configuration changed during registration; retry joining")
			}
			c.Hive, c.IdentityFile, c.KnownHosts = candidate.Hive, candidate.IdentityFile, candidate.KnownHosts
			return nil
		})
		if err != nil {
			return err
		}
	} else if candidate.Hive == "" || candidate.IdentityFile == "" || candidate.KnownHosts == "" {
		return errors.New("paste an invitation to join your first Hive")
	}
	if _, err = joinDirectory(ctx, candidate); err != nil {
		return errors.New("connection saved; Hive is unreachable or this device is no longer registered. Retry joining: " + err.Error())
	}
	if _, e := publisher.Control(dir, "status"); e == nil {
		if _, err = publisher.Control(dir, "reload"); err != nil {
			return err
		}
	}
	if err = joinMachine(ctx, dir, candidate); err != nil {
		return errors.New("registered; finish connection by retrying Join or running bee join: " + err.Error())
	}
	target := machineTarget(candidate)
	if record, ok := connectionRecord(dir, candidate); ok {
		target = record.Target
	}
	return a.emit(map[string]any{"joined": true, "hive": candidate.Hive, "machine": target, "sharing_enabled": candidate.Enabled})
}

func sameRules(a, b []config.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
