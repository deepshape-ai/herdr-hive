package herdr

import "testing"

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
