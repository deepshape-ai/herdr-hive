package gateway

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type memoryStream struct{ bytes.Buffer }

func (*memoryStream) Close() error { return nil }
func nums(v ...uint64) []byte {
	var b []byte
	for _, n := range v {
		b = append(b, number(n)...)
	}
	return b
}
func cell(s string) []byte { return append(str(s), 0, 0, 0, 0, 0) }
func pane(id string) []byte {
	b := str(id)
	return append(b, nums(1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1, 0, 0, 0, 8, 16)...)
}
func full(symbols ...string) []byte {
	b := append([]byte{13}, str("bee-boot")...)
	b = append(b, nums(7, 1, uint64(len(symbols)))...)
	for _, s := range symbols {
		b = append(b, cell(s)...)
	}
	b = append(b, nums(uint64(len(symbols)), 1, 0, 0, 0, 1)...)
	b = append(b, pane("w1:p1")...)
	return append(b, 0, 0, 0, 0, 0) // splits, popup, assets, placements, retained
}
func patch(x uint64, base, rev uint64, symbol string) []byte {
	b := append([]byte{19}, str("bee-boot")...)
	b = append(b, nums(7, base, rev, 1, x, 0, 1)...)
	b = append(b, cell(symbol)...)
	return append(b, 0, 0)
}
func TestWireBoundsAndRoundTrip(t *testing.T) {
	for _, n := range []uint64{0, 250, 251, 65535, 65536, 1 << 32, 1 << 63} {
		d := decoder{b: number(n)}
		if d.num() != n || d.err != nil {
			t.Fatal(n)
		}
	}
	for _, b := range [][]byte{{251}, {252, 0}, {254}, {255}} {
		d := decoder{b: b}
		d.num()
		if d.err == nil {
			t.Fatal("accepted truncated integer")
		}
	}
	var h [4]byte
	binary.LittleEndian.PutUint32(h[:], MaxFrame+1)
	if _, e := Read(bytes.NewReader(h[:])); e == nil {
		t.Fatal("unbounded read")
	}
	var w bytes.Buffer
	if e := Write(&w, control("test", []byte("text"))); e != nil {
		t.Fatal(e)
	}
	p, e := Read(&w)
	if e != nil || !bytes.Equal(p, control("test", []byte("text"))) {
		t.Fatal(e)
	}
}
func TestSurfaceStructuralRewriteAndPatch(t *testing.T) {
	original := full("w1:p1")
	p, e := surface(original, "hive", 9, func(s string) string { return "prefix/" + s })
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(p, str("prefix/w1:p1")) || !bytes.Contains(p, cell("w1:p1")) {
		t.Fatal("identifier rewrite changed terminal contents")
	}
	for i := 0; i < len(original); i++ {
		if _, e := surface(original[:i], "hive", 9, func(s string) string { return s }); e == nil {
			t.Fatalf("accepted truncated surface at %d", i)
		}
	}
	var state completeSurface
	if e := state.update(full("a", "b")); e != nil {
		t.Fatal(e)
	}
	if e := state.update(patch(1, 1, 2, "changed")); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(state.bytes(), cell("changed")) {
		t.Fatal("patch not applied")
	}
	if e := state.update(patch(0, 1, 3, "stale")); e == nil {
		t.Fatal("stale patch accepted")
	}
	if _, e := surface(state.bytes(), "hive", 10, func(s string) string { return s }); e != nil {
		t.Fatal(e)
	}
}
func TestInactivePatchCannotExpandRetainedState(t *testing.T) {
	var state completeSurface
	if e := state.update(full("a", "b", "c")); e != nil {
		t.Fatal(e)
	}
	large := strings.Repeat("x", MaxFrame/2)
	if e := state.update(patch(0, 1, 2, large)); e != nil {
		t.Fatal(e)
	}
	if e := state.update(patch(1, 2, 3, large)); e == nil {
		t.Fatal("retained state exceeded its budget")
	}
}
func TestGraphicsSurviveIncrementalAssetElision(t *testing.T) {
	// A one-pixel RGB asset, referenced by a placement in both full scenes.
	key := append(nums(0, 0), str("w1:p1")...)
	key = append(key, nums(9, 1, 1, 0, 3, 42)...)
	scene := func(withAsset bool) []byte {
		b := full("x")
		b = b[:len(b)-3]
		if withAsset {
			b = append(b, 1)
			b = append(b, key...)
			b = append(b, str("RGB")...)
		} else {
			b = append(b, 0)
		}
		b = append(b, 1)
		b = append(b, key...)
		b = append(b, nums(1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0)...)
		return append(b, 0)
	}
	var state completeSurface
	for _, b := range [][]byte{scene(true), scene(false)} {
		if _, e := surface(b, "hive", 2, func(s string) string { return s }); e != nil {
			t.Fatal(e)
		}
		if e := state.update(b); e != nil {
			t.Fatal(e)
		}
	}
	if !bytes.Contains(state.bytes(), str("RGB")) {
		t.Fatal("live asset was lost on background update")
	}
	if _, e := surface(state.bytes(), "hive", 2, func(s string) string { return "prefix/" + s }); e != nil {
		t.Fatal(e)
	}
	if e := state.update(full("x")); e != nil {
		t.Fatal(e)
	}
	if len(state.assets) != 0 {
		t.Fatal("unused asset retained")
	}
}
func testSession() *session {
	return &session{out: &memoryStream{}, sources: map[string]*backend{}, pending: map[string]*backend{}, deadlines: map[string]time.Time{}, catalog: func() []Source { return nil }, boot: "hive", cancel: func() {}}
}
func responseFrame(boot, id string) []byte {
	b := append([]byte{18}, str(boot)...)
	b = append(b, str(id)...)
	b = append(b, 1)
	return append(b, str(`{"id":"r","result":{"type":"ok"}}`)...)
}
func TestResponsesAreBoundToTheirSource(t *testing.T) {
	g := testSession()
	a := &backend{boot: "a", chunks: map[string][]byte{}}
	b := &backend{boot: "b", chunks: map[string][]byte{}}
	g.pending["r"] = a
	if e := g.server(b, responseFrame("b", "r")); e == nil {
		t.Fatal("cross-source response accepted")
	}
	if e := g.server(a, responseFrame("a", "r")); e != nil {
		t.Fatal(e)
	}
	if e := g.server(a, responseFrame("a", "r")); e == nil {
		t.Fatal("duplicate response accepted")
	}
}
func TestWelcomeIsSingleAndMethodsBounded(t *testing.T) {
	g := testSession()
	b := &backend{methods: map[string]bool{}}
	v, _ := json.Marshal(map[string]any{"generation": 1, "methods": []string{"pane.focus", "untrusted.dynamic"}})
	p := control("endpoint.welcome.v1", v)
	if e := g.server(b, p); e != nil {
		t.Fatal(e)
	}
	if len(b.methods) != 1 {
		t.Fatal(b.methods)
	}
	if e := g.server(b, p); e == nil {
		t.Fatal("repeated welcome accepted")
	}
}
func TestSnapshotPrefixAndFocusIsolation(t *testing.T) {
	g := testSession()
	for _, id := range []string{"a", "b"} {
		snap := emptySnapshot()
		snap["boot_id"] = "same-boot"
		snap["revision"] = float64(7)
		snap["focused_workspace_id"] = "w1"
		snap["workspaces"] = []any{map[string]any{"workspace_id": "w1", "label": "xxx", "focused": true}}
		session := "default"
		if id == "b" {
			session = "research"
		}
		g.sources[id] = &backend{source: Source{ID: id, Name: id, Label: session}, prefix: id + "/", snapshot: snap}
	}
	if e := g.publish(); e != nil {
		t.Fatal(e)
	}
	p, e := Read(g.out)
	if e != nil {
		t.Fatal(e)
	}
	d := decoder{b: p}
	d.num()
	d.text()
	var v map[string]any
	if json.Unmarshal([]byte(d.text()), &v) != nil {
		t.Fatal("bad snapshot")
	}
	ws := array(v["workspaces"])
	if len(ws) != 2 || object(ws[0])["label"] != "[a] xxx" || object(ws[1])["label"] != "[b/research] xxx" || object(ws[1])["focused"] != false {
		t.Fatal(v)
	}
	if object(ws[0])["workspace_id"] != "a/w1" || object(ws[1])["workspace_id"] != "b/w1" {
		t.Fatal("display labels changed resource identity", ws)
	}
	for _, b := range g.sources {
		if object(array(b.snapshot["workspaces"])[0])["label"] != "xxx" {
			t.Fatal("owner workspace label was changed")
		}
	}
	if _, e := io.ReadAll(g.out); e != nil {
		t.Fatal(e)
	}
}

type failedStream struct{ memoryStream }

func (*failedStream) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestSourceWriteFailurePreservesOtherSources(t *testing.T) {
	g := testSession()
	a := &backend{source: Source{ID: "a"}, cancel: func() {}, snapshot: emptySnapshot(), stream: &memoryStream{}}
	b := &backend{source: Source{ID: "b"}, cancel: func() {}, snapshot: emptySnapshot(), stream: &failedStream{}}
	g.sources["a"] = a
	g.sources["b"] = b
	g.active = b
	if e := g.send(b, []byte{18, 1}); e != nil {
		t.Fatal(e)
	}
	if len(g.sources) != 1 || g.active != a {
		t.Fatal("healthy source was disconnected")
	}
}
func TestFenceContinuesAfterSourceDisconnect(t *testing.T) {
	g := testSession()
	a := &backend{source: Source{ID: "a"}, cancel: func() {}, snapshot: emptySnapshot(), stream: &memoryStream{}}
	b := &backend{source: Source{ID: "b"}, cancel: func() {}, snapshot: emptySnapshot(), stream: &memoryStream{}}
	g.sources["a"] = a
	g.sources["b"] = b
	g.active = a
	g.fence = "activation"
	g.fencePending = true
	g.fenceSource = a
	g.remove(a)
	if e := g.publish(); e != nil {
		t.Fatal(e)
	}
	if g.fenceSource != b {
		t.Fatal("fence was orphaned")
	}
	p, e := Read(b.stream)
	if e != nil {
		t.Fatal(e)
	}
	d := decoder{b: p}
	if d.num() != 20 || d.text() != "endpoint.presentation.sync.v1" || d.text() != "activation" {
		t.Fatal("replacement source did not receive fence")
	}
	if e := g.server(b, control("endpoint.presentation.ready.v1", []byte("activation"))); e != nil {
		t.Fatal(e)
	}
	if g.fence != "" || g.fenceSource != nil {
		t.Fatal("fence did not complete")
	}
}

func TestEmptyOpaqueFenceTokenCompletes(t *testing.T) {
	g := testSession()
	b := &backend{source: Source{ID: "a"}, cancel: func() {}, stream: &memoryStream{}}
	g.active = b
	g.sources["a"] = b
	if e := g.client(control("endpoint.presentation.sync.v1", nil)); e != nil {
		t.Fatal(e)
	}
	if !g.fencePending {
		t.Fatal("empty token discarded")
	}
	if e := g.server(b, control("endpoint.presentation.ready.v1", nil)); e != nil {
		t.Fatal(e)
	}
	if g.fencePending {
		t.Fatal("empty token did not complete")
	}
}
