package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"github.com/deepshape-ai/herdr-hive/bee/internal/publisher"
)

// TUI intentionally stays in normal terminal mode: native selection/copy and accessibility work.
func (a App) TUI(ctx context.Context) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("TUI needs a terminal; use bee help for noninteractive commands")
	}
	in := bufio.NewReader(os.Stdin)
	ask := func(label string) (string, error) {
		fmt.Fprint(a.Out, label)
		v, e := in.ReadString('\n')
		return strings.TrimSpace(v), e
	}
	message := ""
	for ctx.Err() == nil {
		c, e := config.Load(a.Dir)
		if e != nil {
			return e
		}
		s, _ := publisher.Control(a.Dir, "status")
		fmt.Fprint(a.Out, "\x1b[2J\x1b[H")
		fmt.Fprintf(a.Out, "Bee · Herdr session sharing\n\nHive: %s\nName: %s\nSharing: %t   Connected: %t\n", c.Hive, c.Name, c.Enabled, s.Connected)
		if s.Error != "" {
			fmt.Fprintln(a.Out, "Connection:", s.Error)
		}
		for _, r := range c.Rules {
			fmt.Fprintln(a.Out, "  •", r.Session)
		}
		for _, share := range s.Shares {
			fmt.Fprintf(a.Out, "  %s / %s → %s\n", share.Name, share.Label, share.ID)
		}
		fmt.Fprintln(a.Out, "\n1  Configure Hive\n2  Change visible name\n3  Select shared sessions\n4  Toggle all sharing\nr  Refresh\nq  Close (sharing continues)")
		if message != "" {
			fmt.Fprintln(a.Out, "\n"+message)
			message = ""
		}
		choice, e := ask("\nChoose: ")
		if e != nil {
			return nil
		}
		var args []string
		switch choice {
		case "q":
			return nil
		case "r":
			continue
		case "1":
			h, e := ask("Hive host:port: ")
			if e != nil {
				return e
			}
			id, e := ask("SSH identity path: ")
			if e != nil {
				return e
			}
			kh, e := ask("Verified known_hosts path: ")
			if e != nil {
				return e
			}
			args = []string{"configure", "--hive", h, "--identity", id, "--known-hosts", kh}
		case "2":
			name, e := ask("Visible name: ")
			if e != nil {
				return e
			}
			args = []string{"name", name}
		case "3":
			sessions, e := herdr.Sessions()
			if e != nil {
				message = e.Error()
				continue
			}
			sessions = sessionChoices(sessions, c.Rules)
			selected := map[string]bool{}
			for _, r := range c.Rules {
				selected[r.Session] = true
			}
			fmt.Fprintln(a.Out, "\nSelected sessions grant full access to registered Hive members.")
			for i, s := range sessions {
				mark := " "
				if selected[s.Name] {
					mark = "x"
				}
				fmt.Fprintf(a.Out, "%d  [%s] %s (running: %t)\n", i+1, mark, s.Name, s.Running)
			}
			raw, e := ask("Toggle session number (empty to return): ")
			if e != nil {
				return e
			}
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > len(sessions) {
				continue
			}
			name := sessions[n-1].Name
			op := "share"
			if selected[name] {
				op = "unshare"
			}
			args = []string{op, name}
		case "4":
			op := "enable"
			if c.Enabled {
				op = "disable"
			}
			args = []string{op}
		default:
			continue
		}
		if e = (App{Dir: a.Dir, Out: io.Discard}).Execute(ctx, args); e != nil {
			message = e.Error()
		} else {
			message = "Saved."
		}
	}
	return ctx.Err()
}

func sessionChoices(live []herdr.Session, rules []config.Rule) []herdr.Session {
	seen := map[string]bool{}
	for _, s := range live {
		seen[s.Name] = true
	}
	out := append([]herdr.Session(nil), live...)
	for _, r := range rules {
		if !seen[r.Session] {
			out = append(out, herdr.Session{Name: r.Session})
			seen[r.Session] = true
		}
	}
	return out
}
