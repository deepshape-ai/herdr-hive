package herdr

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func apiFixture(t *testing.T) (Bound, net.Listener) {
	t.Helper()
	// macOS t.TempDir can exceed sockaddr_un's path budget.
	dir, err := os.MkdirTemp("/tmp", "bee-api-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "herdr.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	cl, err := net.Listen("unix", filepath.Join(dir, "herdr-client.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	b, err := Bind(Session{Name: "shared", Running: true, Socket: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b, l
}

func TestAPIRejectsUnsupportedMethodsAndReplacedSocket(t *testing.T) {
	for _, method := range []string{"server.stop", "plugin.action.invoke", "integration.install", "unknown", "agent.get"} {
		t.Run(method, func(t *testing.T) {
			b, l := apiFixture(t)
			wantCode := "bee_operation_unsupported"
			if method == "agent.get" {
				wantCode = "bee_session_changed"
				l.Close()
				other, err := net.Listen("unix", b.Socket)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				now, err := os.Stat(b.Socket)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("replacement socket: SameFile=%t; oldModTime=%s; newModTime=%s; old=%+v; new=%+v", os.SameFile(b.apiInfo, now), b.apiInfo.ModTime().Format(time.RFC3339Nano), now.ModTime().Format(time.RFC3339Nano), b.apiInfo.Sys(), now.Sys())
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, caller := net.Pipe()
			defer caller.Close()
			deadline, _ := ctx.Deadline()
			if err := caller.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { defer a.Close(); done <- b.ServeAPI(ctx, a) }()
			if err := json.NewEncoder(caller).Encode(map[string]any{"id": "request", "method": method, "params": map[string]any{}}); err != nil {
				t.Fatalf("send %s request: %v", method, err)
			}
			var reply struct {
				ID    string `json:"id"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(caller).Decode(&reply); err != nil {
				t.Fatalf("expected %s for %s; read failed: %v; bound check: %v", wantCode, method, err, b.Check())
			}
			if reply.ID != "request" || reply.Error.Code != wantCode {
				t.Fatalf("expected request ID and %s; got %+v", wantCode, reply)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatalf("ServeAPI did not finish after returning %s: %v", wantCode, ctx.Err())
			}
		})
	}
}

func TestAPIForwardsOneNativeRequestAndPreservesError(t *testing.T) {
	b, l := apiFixture(t)
	upstream := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			upstream <- err.Error()
			return
		}
		defer c.Close()
		line, err := ReadAPILine(c, MaxAPIRequest)
		if err != nil {
			upstream <- err.Error()
			return
		}
		upstream <- string(line)
		io.WriteString(c, "{\"id\":\"native\",\"error\":{\"code\":\"agent_blocked\",\"message\":\"blocked\"}}\n")
	}()
	a, caller := net.Pipe()
	defer caller.Close()
	done := make(chan error, 1)
	go func() { defer a.Close(); done <- b.ServeAPI(context.Background(), a) }()
	// The second request must never escape through an unvalidated raw tunnel.
	io.WriteString(caller, "{\"id\":\"native\",\"method\":\"agent.prompt\",\"params\":{\"target\":\"reviewer\",\"text\":\"hello\"}}\n{\"id\":\"bad\",\"method\":\"server.stop\"}\n")
	line, err := ReadAPILine(caller, MaxAPIRequest)
	if err != nil || !strings.Contains(string(line), "agent_blocked") {
		t.Fatal(string(line), err)
	}
	if got := <-upstream; strings.Contains(got, "server.stop") || !strings.Contains(got, "agent.prompt") {
		t.Fatal(got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAPIWaitCancellationAndReplacementCloseUpstream(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replace"}[replace], func(t *testing.T) {
			b, l := apiFixture(t)
			accepted := make(chan struct{})
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				ReadAPILine(c, MaxAPIRequest)
				close(accepted)
				io.Copy(io.Discard, c)
			}()
			a, caller := net.Pipe()
			defer caller.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { defer a.Close(); done <- b.ServeAPI(ctx, a) }()
			io.WriteString(caller, "{\"id\":\"wait\",\"method\":\"agent.wait\",\"params\":{\"target\":\"reviewer\"}}\n")
			<-accepted
			if replace {
				l.Close()
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("wait not cancelled")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream leaked")
			}
		})
	}
}

func TestAPIInputBoundBeforeDecode(t *testing.T) {
	if _, err := ReadAPILine(strings.NewReader(strings.Repeat("x", 1025)), 1024); err == nil {
		t.Fatal("unbounded input")
	}
}
