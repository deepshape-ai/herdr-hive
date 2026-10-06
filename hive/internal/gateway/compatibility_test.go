package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These fixtures use only Serve/Source and the frozen wire helpers. They can be
// copied to the pre-fix gateway to produce behavioral assertion failures.
type fixtureRequest struct {
	boot, id, method string
	params           map[string]any
}

type fixtureLeg struct {
	conn     net.Conn
	mu       sync.Mutex
	hello    map[string]any
	requests chan fixtureRequest
	failure  chan error
	ready    chan struct{}
}

func (l *fixtureLeg) write(p []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return Write(l.conn, p)
}

func fixtureWelcome(versions ...string) map[string]any {
	version := "0.9.3"
	if len(versions) > 0 {
		version = versions[0]
	}
	capabilities := []string{"surface_interest", "presentation_effects_fence", "health_check"}
	if version != "0.9.0" {
		capabilities = append(capabilities, "surface_delta", "surface_reuse", "surface_scroll")
	}
	return map[string]any{"generation": 1, "server_version": version, "snapshot_codec": "shell.snapshot.v1", "surface_codec": "shell.surface.v1", "input_codec": "shell.input.semantic.v1", "blob_codec": "shell.blob.v1", "methods": []string{"client_shell.surface.set", "workspace.rename", "workspace.focus"}, "capabilities": capabilities}
}

func fixtureSnapshot(id string) []byte {
	s := emptySnapshot()
	s["boot_id"], s["revision"] = "bee-boot", 7
	s["focused_workspace_id"] = "w1"
	s["workspaces"] = []any{map[string]any{"workspace_id": "w1", "label": id, "focused": true}}
	p, _ := json.Marshal(s)
	return control("shell.snapshot.v1", p)
}

func fixtureResponse(boot, id, marker string, final byte) []byte {
	p := append([]byte{18}, str(boot)...)
	p = append(p, str(id)...)
	p = append(p, final)
	return append(p, str(marker)...)
}

func (l *fixtureLeg) reply(r fixtureRequest, marker string) error {
	data, _ := json.Marshal(map[string]any{"id": r.id, "result": map[string]any{"type": "ok", "marker": marker}})
	return l.write(fixtureResponse(r.boot, r.id, string(data), 1))
}

func (l *fixtureLeg) update(p []byte) error {
	// Emulate a newer owner actually selecting every extension offered to it,
	// rather than merely inspecting that hello fields were copied correctly.
	for _, feature := range []string{"reuse", "delta", "scroll", "future"} {
		if l.hello["surface_"+feature] == true {
			return l.write(control("endpoint.surface-"+feature+".v1", []byte("opaque encoded surface")))
		}
	}
	for _, codec := range array(l.hello["surface_codecs"]) {
		if codec != "shell.surface.v1" {
			return l.write(control("endpoint.surface-future.v2", []byte("future binary codec")))
		}
	}
	return l.write(p)
}

type fixtureOwner struct {
	source Source
	opens  chan *fixtureLeg
	count  atomic.Int32
}

func newFixtureOwner(t *testing.T, id, silentFirst string, initial []byte, versions ...string) *fixtureOwner {
	t.Helper()
	f := &fixtureOwner{opens: make(chan *fixtureLeg, 8)}
	f.source = Source{ID: id, Name: id, Label: "default", Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
		first := f.count.Add(1) == 1
		if first && silentFirst == "open" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		gatewayConn, ownerConn := net.Pipe()
		l := &fixtureLeg{conn: ownerConn, requests: make(chan fixtureRequest, 80), failure: make(chan error, 1), ready: make(chan struct{})}
		f.opens <- l
		go func() {
			defer ownerConn.Close()
			err := func() error {
				if first && silentFirst == "hello" {
					<-ctx.Done()
					return ctx.Err()
				}
				p, err := Read(ownerConn)
				if err != nil {
					return err
				}
				d := decoder{b: p}
				if d.num() != 20 || d.text() != "endpoint.hello.v1" || json.Unmarshal([]byte(d.text()), &l.hello) != nil || d.err != nil {
					return fmt.Errorf("owner received invalid hello")
				}
				welcome, _ := json.Marshal(fixtureWelcome(versions...))
				if err := l.write(control("endpoint.welcome.v1", welcome)); err != nil {
					return err
				}
				if !first || silentFirst != "snapshot" {
					if err := l.write(fixtureSnapshot(id)); err != nil {
						return err
					}
				}
				close(l.ready)
				renderStarted := false
				for {
					p, err := Read(ownerConn)
					if err != nil {
						return err
					}
					d := decoder{b: p}
					switch d.num() {
					case 15:
						r := fixtureRequest{boot: d.text()}
						var request map[string]any
						if json.Unmarshal([]byte(d.text()), &request) != nil {
							return fmt.Errorf("invalid owner request")
						}
						r.id, r.method, r.params = stringVal(request["id"]), stringVal(request["method"]), object(request["params"])
						if r.method == "client_shell.surface.set" {
							if r.params["active"] == true && !renderStarted {
								if err := l.write(initial); err != nil {
									return err
								}
								renderStarted = true
							}
							if err := l.reply(r, "interest"); err != nil {
								return err
							}
						} else {
							select {
							case l.requests <- r:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
					case 20:
						kind, data := d.text(), d.text()
						if kind == "endpoint.presentation.sync.v1" {
							if err := l.write(control("endpoint.presentation.ready.v1", []byte(data))); err != nil {
								return err
							}
						}
					}
				}
			}()
			l.failure <- err
		}()
		return gatewayConn, nil
	}}
	return f
}

type fixtureRead struct {
	p   []byte
	err error
}

type fixtureViewer struct {
	t                *testing.T
	conn             net.Conn
	frames           chan fixtureRead
	boot             string
	state            completeSurface
	forbidWithdrawal bool
}

func fixtureHello() map[string]any {
	return map[string]any{"generation": 1, "cell_width_px": 8, "cell_height_px": 16, "surface_size": map[string]any{"cols": 1, "rows": 1}, "pixel_mouse": true, "direct_graphics": true, "endpoint_keybindings": true, "mouse_capture": true, "surface_active": true, "snapshot_codecs": []string{"shell.snapshot.v1"}, "surface_codecs": []string{"shell.surface.v1"}, "input_codecs": []string{"shell.input.semantic.v1"}, "blob_codecs": []string{"shell.blob.v1"}}
}

func newFixtureViewer(t *testing.T, hello map[string]any, sources ...Source) *fixtureViewer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	viewerConn, gatewayConn := net.Pipe()
	h := &fixtureViewer{t: t, conn: viewerConn, frames: make(chan fixtureRead, 256)}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, gatewayConn, func() []Source { return append([]Source(nil), sources...) }) }()
	go func() {
		for {
			p, err := Read(viewerConn)
			select {
			case h.frames <- fixtureRead{p, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		viewerConn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("gateway did not release viewer")
		}
	})
	p, _ := json.Marshal(hello)
	h.write(control("endpoint.hello.v1", p))
	return h
}

func (h *fixtureViewer) write(p []byte) {
	h.t.Helper()
	h.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := Write(h.conn, p); err != nil {
		h.t.Fatal(err)
	}
}

func fixtureJSON(p []byte, kind string) map[string]any {
	d := decoder{b: p}
	if d.num() != 20 || d.text() != kind {
		return nil
	}
	var v map[string]any
	if json.Unmarshal([]byte(d.text()), &v) != nil {
		return nil
	}
	return v
}

func fixtureResponseJSON(p []byte) map[string]any {
	d := decoder{b: p}
	if d.num() != 18 {
		return nil
	}
	d.text()
	id := d.text()
	if !bytes.Equal(d.raw(1), []byte{1}) {
		return nil
	}
	var v map[string]any
	if json.Unmarshal([]byte(d.text()), &v) != nil || v["id"] != id {
		return nil
	}
	return v
}

func (h *fixtureViewer) until(timeout time.Duration, predicate func([]byte) bool) []byte {
	h.t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-h.frames:
			if ev.err != nil {
				h.t.Fatalf("viewer disconnected before expected output: %v", ev.err)
			}
			d := decoder{b: ev.p}
			tag := d.num()
			if tag == 13 || tag == 19 {
				if err := h.state.update(ev.p); err != nil {
					h.t.Fatalf("viewer received an invalid surface transition: %v", err)
				}
			}
			if snap := fixtureJSON(ev.p, "shell.snapshot.v1"); snap != nil {
				h.boot = stringVal(snap["boot_id"])
				if h.forbidWithdrawal && len(array(snap["workspaces"])) == 0 {
					h.t.Fatal("healthy source was withdrawn")
				}
			}
			if predicate(ev.p) {
				return ev.p
			}
		case <-timer.C:
			h.t.Fatal("gateway did not produce expected output before deadline")
		}
	}
}

func (h *fixtureViewer) snapshot(count int, timeout time.Duration) map[string]any {
	p := h.until(timeout, func(p []byte) bool {
		v := fixtureJSON(p, "shell.snapshot.v1")
		return v != nil && len(array(v["workspaces"])) == count
	})
	return fixtureJSON(p, "shell.snapshot.v1")
}

func (h *fixtureViewer) surface(symbol string) []byte {
	return h.until(3*time.Second, func(p []byte) bool {
		d := decoder{b: p}
		tag := d.num()
		return (tag == 13 || tag == 19) && bytes.Contains(h.state.bytes(), cell(symbol))
	})
}

func nextFixtureLeg(t *testing.T, f *fixtureOwner, timeout time.Duration) *fixtureLeg {
	t.Helper()
	select {
	case l := <-f.opens:
		select {
		case <-l.ready:
			return l
		case err := <-l.failure:
			t.Fatal("owner handshake failed", err)
		case <-time.After(timeout):
			t.Fatal("owner hello did not complete")
		}
	case <-time.After(timeout):
		t.Fatal("source was not opened")
	}
	return nil
}

func nextFixtureRequest(t *testing.T, l *fixtureLeg) fixtureRequest {
	t.Helper()
	select {
	case r := <-l.requests:
		return r
	case err := <-l.failure:
		t.Fatal("owner disconnected", err)
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not reach owner")
	}
	return fixtureRequest{}
}

func TestServeNegotiatesOnlyFrozenCodecsAndKeepsRendering(t *testing.T) {
	for _, version := range []string{"0.9.0", "0.9.3", "future"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			hello := fixtureHello()
			if version != "0.9.0" {
				for _, flag := range []string{"surface_reuse", "surface_delta", "surface_scroll"} {
					hello[flag] = true
				}
			}
			if version == "future" {
				hello["surface_future"] = true
				hello["future_optional_capability"] = map[string]any{"enabled": true}
				for _, field := range []string{"snapshot", "surface", "input", "blob"} {
					hello[field+"_codecs"] = []string{"future." + field + ".v2", "shell." + field + map[string]string{"snapshot": ".v1", "surface": ".v1", "input": ".semantic.v1", "blob": ".v1"}[field]}
				}
			}
			owner := newFixtureOwner(t, "a", "", full("initial"), version)
			h := newFixtureViewer(t, hello, owner.source)
			l := nextFixtureLeg(t, owner, 3*time.Second)
			h.snapshot(1, 3*time.Second)
			h.surface("initial")
			h.forbidWithdrawal = true
			for i := uint64(1); i <= 3; i++ {
				symbol := fmt.Sprintf("update-%d", i)
				if err := l.update(patch(0, i, i+1, symbol)); err != nil {
					t.Fatal(err)
				}
				h.surface(symbol)
			}
			if owner.count.Load() != 1 {
				t.Fatal("rendering required a reconnect")
			}
			// Geometry and input semantics are still offered, unlike render flags.
			if l.hello["pixel_mouse"] != true || l.hello["mouse_capture"] != true || l.hello["endpoint_keybindings"] != true || l.hello["cell_width_px"] != float64(8) || l.hello["cell_height_px"] != float64(16) || l.hello["direct_graphics"] != false || l.hello["surface_active"] != false {
				t.Fatal("gateway changed frozen geometry/input semantics", l.hello)
			}
		})
	}
}

func TestServeIgnoresOpaqueSidebandsWithoutLosingSurface(t *testing.T) {
	t.Parallel()
	owner := newFixtureOwner(t, "a", "", full("initial"))
	h := newFixtureViewer(t, fixtureHello(), owner.source)
	l := nextFixtureLeg(t, owner, 3*time.Second)
	h.snapshot(1, 3*time.Second)
	h.surface("initial")
	h.forbidWithdrawal = true
	for _, data := range [][]byte{{0, 255, 1, 128}, bytes.Repeat([]byte{0, 255}, 150<<10)} {
		if err := l.write(control("endpoint.future-sideband.v1", data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.write(patch(0, 1, 2, "after-sideband")); err != nil {
		t.Fatal(err)
	}
	h.surface("after-sideband")
}

func TestServeRejectsInvalidGenerationCodecAndGeometry(t *testing.T) {
	cases := map[string]func(map[string]any){
		"generation":        func(h map[string]any) { h["generation"] = 2 },
		"codec":             func(h map[string]any) { h["surface_codecs"] = []string{"shell.surface.v2"} },
		"zero":              func(h map[string]any) { h["surface_size"] = map[string]any{"cols": 0, "rows": 1} },
		"negative":          func(h map[string]any) { h["surface_size"] = map[string]any{"cols": -1, "rows": 1} },
		"fractional":        func(h map[string]any) { h["surface_size"] = map[string]any{"cols": 1.5, "rows": 1} },
		"missing":           func(h map[string]any) { delete(h, "surface_size") },
		"cell-budget":       func(h map[string]any) { h["surface_size"] = map[string]any{"cols": 257, "rows": 256} },
		"overflow":          func(h map[string]any) { h["surface_size"] = map[string]any{"cols": float64(1 << 63), "rows": 2} },
		"negative-pixels":   func(h map[string]any) { h["cell_width_px"] = -1 },
		"fractional-pixels": func(h map[string]any) { h["cell_height_px"] = 1.5 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			hello := fixtureHello()
			change(hello)
			h := newFixtureViewer(t, hello)
			p := h.until(3*time.Second, func(p []byte) bool { return fixtureJSON(p, "endpoint.welcome.v1") != nil })
			v := fixtureJSON(p, "endpoint.welcome.v1")
			if stringVal(object(v["error"])["message"]) == "" || len(array(v["methods"])) != 0 {
				t.Fatal("invalid hello was not explicitly rejected", v)
			}
		})
	}
}

func TestServeLateResponseSurvivesTimeoutAndConsumerIDReuse(t *testing.T) {
	t.Parallel()
	owner := newFixtureOwner(t, "a", "", full("initial"))
	h := newFixtureViewer(t, fixtureHello(), owner.source)
	l := nextFixtureLeg(t, owner, 3*time.Second)
	snap := h.snapshot(1, 3*time.Second)
	h.surface("initial")
	h.forbidWithdrawal = true
	workspace := stringVal(object(array(snap["workspaces"])[0])["workspace_id"])
	request := func() {
		v, _ := json.Marshal(map[string]any{"id": "reused", "method": "workspace.rename", "params": map[string]any{"workspace_id": workspace, "name": "renamed"}})
		h.write(append(append([]byte{15}, str(h.boot)...), str(string(v))...))
	}
	request()
	old := nextFixtureRequest(t, l)
	p := h.until(35*time.Second, func(p []byte) bool { return fixtureResponseJSON(p) != nil })
	v := fixtureResponseJSON(p)
	if v["id"] != "reused" || !strings.Contains(stringVal(object(v["error"])["message"]), "timed out") {
		t.Fatal("operation did not time out once", v)
	}
	// A legal 75-second owner operation must not become unsolicited merely
	// because an arbitrary 30-second tombstone grace period has passed.
	time.Sleep(45 * time.Second)
	request()
	current := nextFixtureRequest(t, l)
	if err := l.write(fixtureResponse(old.boot, old.id, `{"id":"old","result":`, 0)); err != nil {
		t.Fatal(err)
	}
	if err := l.write(fixtureResponse(old.boot, old.id, `{"marker":"old"}}`, 1)); err != nil {
		t.Fatal(err)
	}
	if err := l.reply(current, "current"); err != nil {
		t.Fatal(err)
	}
	p = h.until(3*time.Second, func(p []byte) bool { return fixtureResponseJSON(p) != nil })
	v = fixtureResponseJSON(p)
	if v["id"] != "reused" || object(v["result"])["marker"] != "current" {
		t.Fatal("late response reached the reused consumer request", v)
	}
	if err := l.write(patch(0, 1, 2, "still-healthy")); err != nil {
		t.Fatal(err)
	}
	h.surface("still-healthy")
	if owner.count.Load() != 1 {
		t.Fatal("late response caused source reconnect")
	}
}

func TestServeSilentInitializationIsIsolatedAndRecovers(t *testing.T) {
	for _, stage := range []string{"open", "hello", "snapshot"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			healthy := newFixtureOwner(t, "a", "", full("healthy"))
			silent := newFixtureOwner(t, "b", stage, full("recovered"))
			h := newFixtureViewer(t, fixtureHello(), healthy.source, silent.source)
			l := nextFixtureLeg(t, healthy, 3*time.Second)
			h.snapshot(1, 3*time.Second)
			h.surface("healthy")
			h.forbidWithdrawal = true
			if stage != "open" {
				select {
				case <-silent.opens:
				case <-time.After(3 * time.Second):
					t.Fatal("silent source was not opened")
				}
			}
			nextFixtureLeg(t, silent, 13*time.Second)
			h.snapshot(2, 3*time.Second)
			if err := l.write(patch(0, 1, 2, "healthy-during-recovery")); err != nil {
				t.Fatal(err)
			}
			h.surface("healthy-during-recovery")
			if healthy.count.Load() != 1 || silent.count.Load() != 2 {
				t.Fatal("initialization recovery displaced healthy source")
			}
		})
	}
}

func fixtureGraphicsSurface(symbol string) []byte {
	key := append(nums(0, 0), str("w1:p1")...)
	key = append(key, nums(9, 1, 1, 0, 3, 42)...)
	b := full(symbol)
	b = b[:len(b)-3]
	b = append(b, 1)
	b = append(b, key...)
	b = append(b, str("RGB")...)
	b = append(b, 1)
	b = append(b, key...)
	b = append(b, nums(1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0)...)
	return append(b, 0)
}

func fixturePanePatch(base, rev uint64, symbol string) []byte {
	b := patch(0, base, rev, symbol)
	b = b[:len(b)-2]
	b = append(b, 1)
	b = append(b, pane("w1:p1")...)
	return append(b, 0)
}

func fixtureOperation(h *fixtureViewer, id, method string, params map[string]any) {
	v, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	h.write(append(append([]byte{15}, str(h.boot)...), str(string(v))...))
}

func TestServePlainPatchIsIncrementalAndSourceSwitchKeepsCompleteGraphics(t *testing.T) {
	t.Parallel()
	a := newFixtureOwner(t, "a", "", fixtureGraphicsSurface("initial-a"))
	b := newFixtureOwner(t, "b", "", full("initial-b"))
	h := newFixtureViewer(t, fixtureHello(), a.source, b.source)
	la := nextFixtureLeg(t, a, 3*time.Second)
	lb := nextFixtureLeg(t, b, 3*time.Second)
	snap := h.snapshot(2, 3*time.Second)
	ids := map[string]string{}
	for _, ws := range array(snap["workspaces"]) {
		label := stringVal(object(ws)["label"])
		if strings.HasPrefix(label, "[a]") {
			ids["a"] = stringVal(object(ws)["workspace_id"])
		} else if strings.HasPrefix(label, "[b]") {
			ids["b"] = stringVal(object(ws)["workspace_id"])
		}
	}
	focus := func(name, requestID string, leg *fixtureLeg) {
		fixtureOperation(h, requestID, "workspace.focus", map[string]any{"workspace_id": ids[name]})
		r := nextFixtureRequest(t, leg)
		if err := leg.reply(r, "focus"); err != nil {
			t.Fatal(err)
		}
	}
	focus("a", "focus-a", la)
	if err := la.write(fixtureGraphicsSurface("initial-a")); err != nil {
		t.Fatal(err)
	}
	h.surface("initial-a")
	h.forbidWithdrawal = true
	if err := la.write(fixturePanePatch(1, 2, "incremental-a")); err != nil {
		t.Fatal(err)
	}
	p := h.surface("incremental-a")
	d := decoder{b: p}
	if d.num() != 19 || d.text() != h.boot || !bytes.Contains(p, str(strings.TrimSuffix(ids["a"], "w1")+"w1:p1")) || len(p) >= len(h.state.bytes()) {
		t.Fatal("active ordinary patch was expanded, lost IDs, or crossed boot identity")
	}
	if err := lb.write(full("initial-b")); err != nil {
		t.Fatal(err)
	}
	focus("b", "focus-b", lb)
	p = h.surface("initial-b")
	d = decoder{b: p}
	if d.num() != 13 {
		t.Fatal("source switch did not establish a complete baseline")
	}
	if err := la.write(fixturePanePatch(2, 3, "background-a")); err != nil {
		t.Fatal(err)
	}
	// A snapshot from the same leg is an ordered barrier after its hidden patch.
	if err := la.write(fixtureSnapshot("a")); err != nil {
		t.Fatal(err)
	}
	h.snapshot(2, 3*time.Second)
	h.surface("initial-b")
	focus("a", "focus-back", la)
	p = h.surface("background-a")
	d = decoder{b: p}
	if d.num() != 13 || !bytes.Contains(p, str("RGB")) {
		t.Fatal("switch lost hidden patch state or retained graphics assets")
	}
	if err := la.write(fixturePanePatch(3, 4, "incremental-after-switch")); err != nil {
		t.Fatal(err)
	}
	p = h.surface("incremental-after-switch")
	d = decoder{b: p}
	if d.num() != 19 {
		t.Fatal("new source baseline did not resume incremental output")
	}
}

func TestServeRejectsCrossSourceStaleBootDuplicateAndUnsolicitedResponses(t *testing.T) {
	for _, attack := range []string{"cross-source", "stale-boot", "duplicate", "unsolicited"} {
		t.Run(attack, func(t *testing.T) {
			t.Parallel()
			a := newFixtureOwner(t, "a", "", full("a"))
			b := newFixtureOwner(t, "b", "", full("b"))
			h := newFixtureViewer(t, fixtureHello(), a.source, b.source)
			la := nextFixtureLeg(t, a, 3*time.Second)
			lb := nextFixtureLeg(t, b, 3*time.Second)
			snap := h.snapshot(2, 3*time.Second)
			var aID string
			for _, ws := range array(snap["workspaces"]) {
				if strings.HasPrefix(stringVal(object(ws)["label"]), "[a]") {
					aID = stringVal(object(ws)["workspace_id"])
				}
			}
			fixtureOperation(h, "r", "workspace.rename", map[string]any{"workspace_id": aID, "name": "rename"})
			r := nextFixtureRequest(t, la)
			target, boot, id := la, r.boot, r.id
			switch attack {
			case "cross-source":
				target = lb
			case "stale-boot":
				boot = "stale-boot"
			case "unsolicited":
				id = "never-issued"
			case "duplicate":
				if err := la.reply(r, "valid"); err != nil {
					t.Fatal(err)
				}
				h.until(3*time.Second, func(p []byte) bool { return fixtureResponseJSON(p)["id"] == "r" })
			}
			if err := target.write(responseFrame(boot, id)); err != nil {
				t.Fatal(err)
			}
			h.snapshot(1, 3*time.Second)
		})
	}
}
