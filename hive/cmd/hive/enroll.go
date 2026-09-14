package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/deepshape-ai/herdr-hive/hive/internal/enrollment"
)

func enroll(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hive enroll issue|list|revoke [ID] --tokens PATH")
	}
	op := args[0]
	args = args[1:]
	id := ""
	if op == "revoke" && len(args) > 0 {
		id = args[0]
		args = args[1:]
	}
	if op != "issue" && op != "list" && op != "revoke" {
		return errors.New("unknown enrollment operation")
	}
	f := flag.NewFlagSet("enroll "+op, flag.ContinueOnError)
	path := f.String("tokens", "", "administrator token file (required)")
	var state, label, address string
	// Keep each subcommand's accepted options explicit.
	var ttlValue time.Duration
	var maxUses int
	if op == "issue" {
		f.DurationVar(&ttlValue, "ttl", 0, "validity (e.g. 24h; 0 means no expiry)")
		f.IntVar(&maxUses, "max-uses", 0, "maximum distinct devices (0 means unlimited)")
		f.StringVar(&label, "label", "", "administrator label")
		f.StringVar(&address, "hive", "", "member-reachable HOST:PORT; include a pasteable invitation")
		f.StringVar(&state, "state-dir", "", "running Hive state directory (required with --hive)")
	}
	if op == "list" {
		f.StringVar(&state, "state-dir", "", "optional service state directory for usage counts")
	}
	if e := f.Parse(args); e != nil {
		return e
	}
	if *path == "" || f.NArg() != 0 || (op == "revoke" && id == "") {
		return errors.New("--tokens PATH and, for revoke, ID are required")
	}
	var hostKey string
	if op == "issue" && (address != "" || state != "") {
		var err error
		hostKey, err = invitationHost(address, state)
		if err != nil {
			return err
		}
	}
	lock, e := os.OpenFile(*path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	ts, e := enrollment.LoadTokens(*path)
	if op == "issue" && os.IsNotExist(e) {
		ts = []enrollment.Token{}
		e = nil
	}
	if e != nil {
		return e
	}
	switch op {
	case "issue":
		t, secret, e := enrollment.Generate(ttlValue, maxUses, label)
		if e != nil {
			return e
		}
		ts = append(ts, t)
		if e = enrollment.SaveTokens(*path, ts); e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			enrollment.Token
			Secret     string `json:"secret"`
			Invitation string `json:"invitation,omitempty"`
		}{t, secret, encodeInvitation(address, hostKey, secret)})
	case "list":
		uses := enrollment.Uses{}
		if state != "" {
			uses, e = enrollment.LoadUses(filepath.Join(state, "enrollment.json"))
			if e != nil {
				return e
			}
		}
		type entry struct {
			enrollment.Token
			Uses *int `json:"uses,omitempty"`
		}
		out := []entry{}
		for _, t := range ts {
			v := entry{Token: t}
			if state != "" {
				n := uses[t.ID].Uses
				v.Uses = &n
			}
			out = append(out, v)
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	case "revoke":
		for i, t := range ts {
			if t.ID == id {
				ts = append(ts[:i], ts[i+1:]...)
				if e = enrollment.SaveTokens(*path, ts); e != nil {
					return e
				}
				return json.NewEncoder(os.Stdout).Encode(map[string]string{"revoked": id})
			}
		}
		return fmt.Errorf("token %q not found", id)
	}
	return nil
}

func invitationHost(address, state string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	if err != nil || pe != nil || host == "" || n < 1 || n > 65535 || state == "" || len(host) > 253 {
		return "", errors.New("invitation requires --hive HOST:PORT and --state-dir PATH of an initialized Hive")
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-:", c)) {
			return "", errors.New("invalid invitation hostname")
		}
	}
	if strings.HasPrefix(host, "-") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", errors.New("invalid invitation hostname")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return "", errors.New("use a Hive address reachable by members, not a wildcard listen address")
	}
	b, err := os.ReadFile(filepath.Join(state, "host_key"))
	if err != nil {
		return "", fmt.Errorf("start Hive once before issuing invitations: %w", err)
	}
	key, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey()))), nil
}

func encodeInvitation(address, hostKey, token string) string {
	if address == "" {
		return ""
	}
	b, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Hive    string `json:"hive"`
		HostKey string `json:"host_key"`
		Token   string `json:"token"`
	}{1, address, hostKey, token})
	return "hinv1-" + base64.RawURLEncoding.EncodeToString(b)
}
