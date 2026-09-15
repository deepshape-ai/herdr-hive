package herdr

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteContextCannotUseLocalSessionOrCaller(t *testing.T) {
	for _, cmd := range [][]string{
		{"sh", "-c", "herdr agent list"},
		{"herdr", "server", "stop"},
		{"herdr", "agent", "attach", "reviewer"},
		{"herdr", "agent", "list", "--session", "private"},
		{"herdr", "agent", "list", "--session=private"},
		{"herdr", "pane", "split", "--current"},
		{"herdr", "workspace", "create", "--cwd", "relative"},
	} {
		if ValidateRemoteCommand(cmd) == nil {
			t.Fatal("accepted", cmd)
		}
	}
	args := []string{"herdr", "agent", "start", "reviewer", "--kind", "codex", "--pane", "w1:p1", "--", "--session", "native-agent-argument"}
	if err := ValidateRemoteCommand(args); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"HERDR_SESSION", "HERDR_CONFIG_PATH", "HERDR_PANE_ID", "HERDR_WORKSPACE_ID", "HERDR_SOCKET_PATH", "HERDR_PLUGIN_CONTEXT_JSON"} {
		t.Setenv(k, "local-private")
	}
	cmd := RemoteCommand(context.Background(), "/tmp/remote.sock", args)
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "HERDR_") && env != "HERDR_SOCKET_PATH=/tmp/remote.sock" {
			t.Fatal("leaked caller", env)
		}
	}
	if strings.Join(cmd.Args[1:], "|") != strings.Join(args[1:], "|") {
		t.Fatal("changed native arguments")
	}
}

func TestRemoteValidationPreservesNativePayloads(t *testing.T) {
	for _, args := range [][]string{
		{"herdr", "pane", "run", "w1:p1", "tool", "--cwd", "relative"},
		{"herdr", "pane", "send-text", "w1:p1", "--current"},
		{"herdr", "pane", "send-text", "w1:p1", "--file"},
		{"herdr", "agent", "prompt", "reviewer", "--cwd"},
		{"herdr", "workspace", "create", "--label", "--cwd"},
		{"herdr", "tab", "create", "--label", "--current"},
		{"herdr", "pane", "move", "w1:p1", "--new-tab", "--label", "--file"},
	} {
		if err := ValidateRemoteCommand(args); err != nil {
			t.Errorf("rejected native payload %q: %v", args, err)
		}
	}
}

func TestRemoteValidationStillRejectsActualContextOptions(t *testing.T) {
	for _, args := range [][]string{
		{"herdr", "agent", "explain", "--file", "/tmp/local", "--agent", "codex"},
		{"herdr", "pane", "resize", "--current", "--direction", "up"},
		{"herdr", "pane", "split", "w1:p1", "--cwd", "relative"},
		{"herdr", "tab", "create", "--cwd", "relative"},
		{"herdr", "workspace", "create", "--label", "--", "--cwd", "relative"},
		// Herdr consumes session overrides globally, even in option values.
		{"herdr", "workspace", "create", "--label", "--session=private"},
		{"herdr", "pane", "run", "w1:p1", "tool", "--remote", "other"},
	} {
		if err := ValidateRemoteCommand(args); err == nil {
			t.Errorf("accepted context override %q", args)
		}
	}
}
