package herdr

import (
	"strings"
	"testing"
)

func TestNativeRequestsAndShellRejection(t *testing.T) {
	for _, tc := range []struct{ command, script, kind string }{
		{"/bin/sh -s", "uname -s\nuname -m\n", "platform"},
		{"/bin/sh -s", "test -x '/herdr' && '/herdr' status client --json", "status-client"},
		{"/bin/sh -s", "'/herdr' status server --json", "status-server"},
		{"exec '/herdr' remote-client-bridge", "", "client"},
	} {
		a, e := Resolve(tc.command, tc.script)
		if e != nil || a.Kind != tc.kind {
			t.Fatalf("%q: %+v %v", tc.command, a, e)
		}
	}
	for _, command := range []string{"id", "sh", "exec '/herdr' remote-client-bridge; id", "'/herdr' --session private status server --json", "scp -t /tmp/x", "$(touch /tmp/x)", "test -x '/herdr' && '/herdr' server stop"} {
		if _, e := Resolve(command, ""); e == nil {
			t.Fatalf("accepted %q", command)
		}
	}
	if _, e := Resolve("/bin/sh -s", "uname -s\nuname -m\nid"); e == nil {
		t.Fatal("accepted appended shell command")
	}
}

func TestFramedRequestsForHerdr091AndNewer(t *testing.T) {
	for _, tc := range []struct{ command, script, kind, output string }{
		{"/bin/sh -s", framedCommandPrefix + "uname -s\nuname -m\n", "platform", ""},
		{framedCommandPrefix + "command -v herdr", "", "", "/herdr\n"},
		{"/bin/sh -s", framedCommandPrefix + "test -x '/herdr' && '/herdr' status client --json", "status-client", ""},
		{framedCommandPrefix + "exec /herdr remote-client-bridge", "", "client", ""},
	} {
		a, err := Resolve(tc.command, tc.script)
		var response strings.Builder
		beginErr := a.BeginResponse(&response)
		if err != nil || beginErr != nil || a.Kind != tc.kind || a.Output != tc.output || response.String() != framedOutputPrefix {
			t.Fatalf("%q: %+v %v", tc.command, a, err)
		}
	}
}

func TestDiscoveryAcceptsHerdr090AndNewerWithoutExecutingShell(t *testing.T) {
	for _, version := range []string{"0.9.0", "0.9.0+linux.x86-64", "0.9.1", "0.9.1--preview", "0.10.0-rc.1", "1.0.0"} {
		a, err := Resolve("/bin/sh -s", framedCommandPrefix+discoveryScript(version))
		var response strings.Builder
		beginErr := a.BeginResponse(&response)
		if err != nil || beginErr != nil || a.Output != "/herdr\n" || response.String() != framedOutputPrefix {
			t.Fatalf("version %s: %+v %v", version, a, err)
		}
	}
	for _, version := range []string{
		"0.8.99", "0.9.0-rc.1", "invalid", "01.9.0", "0.9.1-.",
		"0.9.1-foo..bar", "0.9.1-01", "0.9.1+foo..bar", "0.9.1+foo+bar",
	} {
		if _, err := Resolve("/bin/sh -s", discoveryScript(version)); err == nil {
			t.Fatalf("accepted unsupported version %q", version)
		}
	}
	for _, script := range []string{
		discoveryScript("0.9.1") + "\nid",
		strings.Replace(discoveryScript("0.9.1"), `emit "$home/.local/bin/herdr"`, `emit "$home/$(id)/herdr"`, 1),
		strings.Replace(discoveryScript("0.9.1"), "emit() {", "run() {", 1),
		strings.Replace(discoveryScript("0.9.1"), "    path=$1\n", "", 1),
		strings.Replace(discoveryScript("0.9.1"), "fi\nif [ -n \"$user\" ]; then", "if [ -n \"$user\" ]; then", 1),
		strings.Replace(discoveryScript("0.9.1"), "if [ -n \"$user\" ]; then", "if [ -n \"$home\" ]; then", 1),
		strings.Replace(discoveryScript("0.9.1"), `emit "$home/.local/bin/herdr"`, `emit "$user/.local/bin/herdr"`, 1),
		strings.Replace(discoveryScript("0.9.1"), `emit "/run/current-system/sw/bin/herdr"`, `emit "$home/bin/herdr"`, 1),
	} {
		if _, err := Resolve("/bin/sh -s", script); err == nil {
			t.Fatal("accepted modified discovery shell")
		}
	}
}

func discoveryScript(version string) string {
	return `home=${HOME:-}
user=${USER:-}
version=` + version + `
emit() {
    path=$1
    if [ -n "$path" ] && [ -x "$path" ]; then
        printf '%s\n' "$path"
    fi
}
if [ -n "$home" ]; then
    emit "$home/.local/bin/herdr"
fi
if [ -n "$user" ]; then
    emit "/etc/profiles/per-user/$user/bin/herdr"
fi
emit "/run/current-system/sw/bin/herdr"`
}
