package herdr

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// ValidateRemoteCommand keeps local-only commands and session overrides from
// silently escaping the selected remote context. Herdr still parses arguments.
func ValidateRemoteCommand(args []string) error {
	if len(args) < 3 || args[0] != "herdr" {
		return errors.New("usage: bee on BEE/SESSION -- herdr agent|pane|tab|workspace ...")
	}
	allowed := map[string]string{
		"agent":     " list get read send-keys prompt rename focus wait start explain ",
		"pane":      " list current get layout process-info neighbor focus resize zoom read rename input split swap move close send-text send-keys wait-output run ",
		"tab":       " list create get focus rename close ",
		"workspace": " list create get focus rename close ",
		"api":       " snapshot ",
		"status":    " server ",
	}
	if !strings.Contains(allowed[args[1]], " "+args[2]+" ") {
		return errors.New("command is not supported by bee on; use session-scoped Herdr agent, pane, tab or workspace commands")
	}
	// Herdr consumes these globally, including tokens used as option values,
	// but stops at the first literal -- before dispatching the subcommand.
	for _, arg := range args[3:] {
		if arg == "--" {
			break
		}
		if arg == "--session" || strings.HasPrefix(arg, "--session=") || arg == "--remote" || strings.HasPrefix(arg, "--remote=") {
			return errors.New("--session and --remote cannot override the selected Bee target")
		}
	}
	// Only inspect options belonging to commands with a local-context boundary.
	// Other commands may treat these exact tokens as prompt, label or shell text.
	var valueOptions string
	var current, cwd, file bool
	switch args[1] + " " + args[2] {
	case "agent explain":
		file = true
		valueOptions = " --agent --format "
	case "workspace create", "tab create":
		cwd = true
		valueOptions = " --cwd --label --env --workspace "
	case "pane current", "pane layout", "pane process-info", "pane neighbor", "pane resize", "pane zoom", "pane input", "pane split", "pane swap":
		current = true
		cwd = args[2] == "split"
		valueOptions = " --pane --direction --amount --right-click --ratio --cwd --env --source-pane --target-pane "
	default:
		return nil
	}
	for i := 3; i < len(args); i++ {
		arg := args[i]
		if (current && arg == "--current") || (file && arg == "--file") {
			return errors.New("remote commands require explicit target IDs; --current and local --file are unavailable")
		}
		if cwd && arg == "--cwd" {
			if i+1 >= len(args) || !filepath.IsAbs(args[i+1]) {
				return errors.New("--cwd must be an absolute path on the target Bee")
			}
		}
		if strings.Contains(valueOptions, " "+arg+" ") {
			i++
		}
	}
	return nil
}

func RemoteCommand(ctx context.Context, socket string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary(), args[1:]...)
	cmd.Env = append(cleanEnv(), "HERDR_SOCKET_PATH="+socket)
	return cmd
}
