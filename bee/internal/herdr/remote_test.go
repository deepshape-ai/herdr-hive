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

func TestRemotePaneFocusRejectsCurrentSelector(t *testing.T) {
	for _, args := range [][]string{
		{"herdr", "pane", "focus", "--direction", "right", "--current"},
		{"herdr", "pane", "focus", "--pane", "w1:p1", "--direction", "right", "--current"},
		{"herdr", "pane", "focus", "--direction", "right", "--current", "--pane", "w1:p1"},
	} {
		t.Run(strings.Join(args[3:], " "), func(t *testing.T) {
			if err := ValidateRemoteCommand(args); err == nil {
				t.Fatal("accepted local-context selector", args)
			}
		})
	}
	for _, args := range [][]string{
		{"herdr", "pane", "focus", "--pane", "w1:p1", "--direction", "right"},
		{"herdr", "pane", "focus", "--direction", "left", "--pane", "w1:p2"},
	} {
		if err := ValidateRemoteCommand(args); err != nil {
			t.Fatalf("rejected explicit remote focus %q: %v", args, err)
		}
	}
}

func TestRemoteValidationNativeContextGrammar(t *testing.T) {
	for _, args := range [][]string{
		{"herdr", "pane", "current", "--pane", "w1:p1", "--current"},
		{"herdr", "pane", "layout", "--pane", "w1:p1", "--current"},
		{"herdr", "pane", "process-info", "--pane", "w1:p1", "--current"},
		{"herdr", "pane", "neighbor", "--direction", "right", "--pane", "w1:p1", "--current"},
		{"herdr", "pane", "resize", "--pane", "w1:p1", "--direction", "up", "--amount", "0.1", "--current"},
		{"herdr", "pane", "zoom", "w1:p1", "--on", "--current"},
		{"herdr", "pane", "input", "--current", "--right-click=pane"},
		{"herdr", "pane", "split", "w1:p1", "--direction", "down", "--right-click=pane", "--current"},
		{"herdr", "pane", "swap", "--pane", "w1:p1", "--direction", "right", "--current"},
		{"herdr", "pane", "split", "w1:p1", "--direction", "down", "--env", "KEY=--current", "--cwd", "relative"},
		{"herdr", "tab", "create", "--workspace", "w1", "--label", "--current", "--cwd", "relative"},
		{"herdr", "agent", "explain", "--agent", "--current", "--file", "/tmp/local"},
		{"herdr", "workspace", "create", "--cwd=relative"},
		{"herdr", "tab", "create", "--cwd=relative"},
		{"herdr", "pane", "split", "w1:p1", "--env=KEY=--current", "--cwd=relative"},
		{"herdr", "agent", "explain", "--file=/tmp/local", "--agent=codex"},
		{"herdr", "pane", "focus", "--pane=w1:p1", "--direction=right", "--current"},
		{"herdr", "pane", "focus", "--current=true", "--pane=w1:p1"},
		// These native context parsers do not use -- as an option terminator.
		{"herdr", "pane", "focus", "--direction", "right", "--", "--current"},
		{"herdr", "tab", "create", "--label", "--", "--cwd", "relative"},
	} {
		t.Run("reject/"+strings.Join(args[1:], " "), func(t *testing.T) {
			if err := ValidateRemoteCommand(args); err == nil {
				t.Fatal("accepted actual local-context option", args)
			}
		})
	}
	for _, args := range [][]string{
		{"herdr", "pane", "current", "--pane", "w1:p1"},
		{"herdr", "pane", "layout", "--pane", "w1:p1"},
		{"herdr", "pane", "process-info", "--pane", "w1:p1"},
		{"herdr", "pane", "neighbor", "--pane", "w1:p1", "--direction", "right"},
		{"herdr", "pane", "resize", "--pane", "w1:p1", "--direction", "up", "--amount", "0.1"},
		{"herdr", "pane", "zoom", "w1:p1", "--on"},
		{"herdr", "pane", "input", "--pane=w1:p1", "--right-click=pane"},
		{"herdr", "pane", "split", "w1:p1", "--direction", "down", "--right-click=pane", "--env", "KEY=--current", "--cwd", "/srv/project"},
		{"herdr", "pane", "swap", "--source-pane", "w1:p1", "--target-pane", "w1:p2"},
		{"herdr", "workspace", "create", "--label", "--file", "--cwd", "/srv/project"},
		{"herdr", "tab", "create", "--workspace", "w1", "--label", "--current", "--cwd", "/srv/project"},
		{"herdr", "agent", "explain", "reviewer", "--format", "json"},
		{"herdr", "pane", "send-text", "w1:p1", "--current", "--file"},
		{"herdr", "pane", "run", "w1:p1", "tool", "--current", "--file"},
		{"herdr", "pane", "rename", "w1:p1", "--current", "--file"},
		{"herdr", "agent", "prompt", "reviewer", "--current", "--file"},
		{"herdr", "workspace", "create", "--label=--file", "--cwd=/srv/project"},
		{"herdr", "tab", "create", "--label=--current", "--cwd=/srv/project"},
		{"herdr", "agent", "explain", "--agent=--file", "--format=json"},
		// --machine is prefix-only; the native command owns literal prompt text.
		{"herdr", "agent", "prompt", "reviewer", "--machine", "literal-prompt"},
	} {
		t.Run("allow/"+strings.Join(args[1:], " "), func(t *testing.T) {
			if err := ValidateRemoteCommand(args); err != nil {
				t.Fatalf("rejected native arguments %q: %v", args, err)
			}
		})
	}
	if err := ValidateRemoteCommand([]string{"herdr", "--machine", "other", "agent", "list"}); err == nil {
		t.Fatal("accepted global prefix in place of the required command group")
	}
}
