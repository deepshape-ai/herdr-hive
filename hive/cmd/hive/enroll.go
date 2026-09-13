package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

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
	var state, label string
	// Keep each subcommand's accepted options explicit.
	var ttlValue time.Duration
	var maxUses int
	if op == "issue" {
		f.DurationVar(&ttlValue, "ttl", 0, "validity (e.g. 24h; 0 means no expiry)")
		f.IntVar(&maxUses, "max-uses", 0, "maximum distinct devices (0 means unlimited)")
		f.StringVar(&label, "label", "", "administrator label")
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
			Secret string `json:"secret"`
		}{t, secret})
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
