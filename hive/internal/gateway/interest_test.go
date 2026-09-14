package gateway

import (
	"bytes"
	"encoding/json"
	"testing"
)

func clientRequest(g *session, id, method string, params any) []byte {
	v, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	return append(append([]byte{15}, str(g.boot)...), str(string(v))...)
}

func TestResizeOnlyReachesVisibleSource(t *testing.T) {
	g := testSession()
	a, b := &memoryStream{}, &memoryStream{}
	g.sources["a"] = &backend{stream: a}
	g.sources["b"] = &backend{stream: b}
	g.active = g.sources["a"]
	p := nums(12, 8, 16, 90, 25, 0)
	if err := g.client(p); err != nil {
		t.Fatal(err)
	}
	if a.Len() != 0 || b.Len() != 0 {
		t.Fatal("hidden viewer resized a publisher")
	}
	g.surfaceActive = true
	if err := g.client(p); err != nil {
		t.Fatal(err)
	}
	got, err := Read(a)
	if err != nil || !bytes.Equal(got, p) || b.Len() != 0 {
		t.Fatal("resize escaped the visible source")
	}
}

func interestSource(id string) *backend {
	return &backend{source: Source{ID: id}, boot: id, prefix: id + "/", stream: &memoryStream{}, snapshot: emptySnapshot(), methods: map[string]bool{"client_shell.surface.set": true}, chunks: map[string][]byte{}, cancel: func() {}}
}

func readInterest(t *testing.T, b *backend, active bool) string {
	t.Helper()
	p, err := Read(b.stream)
	if err != nil {
		t.Fatal(err)
	}
	d := decoder{b: p}
	if d.num() != 15 || d.text() != b.boot {
		t.Fatal("invalid interest request")
	}
	var v map[string]any
	if json.Unmarshal([]byte(d.text()), &v) != nil || v["method"] != "client_shell.surface.set" || object(v["params"])["active"] != active {
		t.Fatal(v)
	}
	return stringVal(v["id"])
}

func TestInterestFollowsVisibilityAndConsumesAcknowledgements(t *testing.T) {
	g := testSession()
	a, b := interestSource("a"), interestSource("b")
	g.sources["a"], g.sources["b"] = a, b
	g.active = a
	g.resize = nums(12, 8, 16, 80, 24, 0)
	if err := g.publish(); err != nil {
		t.Fatal(err)
	}
	if a.stream.(*memoryStream).Len() != 0 || b.stream.(*memoryStream).Len() != 0 {
		t.Fatal("hidden subscriptions activated")
	}
	g.surfaceActive = true
	if err := g.publish(); err != nil {
		t.Fatal(err)
	}
	p, err := Read(a.stream)
	if err != nil || !bytes.Equal(p, g.resize) {
		t.Fatal("latest size must precede activation")
	}
	id := readInterest(t, a, true)
	before := g.out.(*memoryStream).Len()
	if err := g.server(a, responseFrame(a.boot, id)); err != nil {
		t.Fatal(err)
	}
	if g.out.(*memoryStream).Len() != before || len(g.pending) != 0 {
		t.Fatal("internal acknowledgement leaked or retained")
	}
	g.active = b
	if err := g.publish(); err != nil {
		t.Fatal(err)
	}
	readInterest(t, a, false)
	p, err = Read(b.stream)
	if err != nil || !bytes.Equal(p, g.resize) {
		t.Fatal("replacement missed cached size")
	}
	readInterest(t, b, true)
	g.surfaceActive = false
	if err := g.publish(); err != nil {
		t.Fatal(err)
	}
	readInterest(t, b, false)
	if a.surfaceActive || b.surfaceActive {
		t.Fatal("hidden source retained interest")
	}
	if err := g.client(clientRequest(g, interestPrefix+"1", "client_shell.surface.set", map[string]any{"active": true})); err == nil {
		t.Fatal("client forged internal request ID")
	}
}

func TestActivationWriteFailurePreservesHealthySource(t *testing.T) {
	for _, resize := range []bool{true, false} {
		name := "interest"
		if resize {
			name = "resize"
		}
		t.Run(name, func(t *testing.T) {
			g := testSession()
			a, b := interestSource("a"), interestSource("b")
			a.stream = &failedStream{}
			g.sources["a"], g.sources["b"] = a, b
			g.surfaceActive = true
			if resize {
				g.resize = nums(12, 8, 16, 80, 24, 0)
			}
			if err := g.publish(); err != nil {
				t.Fatal(err)
			}
			if len(g.sources) != 1 || g.active != b || !b.surfaceActive {
				t.Fatal("activation failure lost healthy source")
			}
		})
	}
}

func TestMalformedResizeCannotReplaceCachedSize(t *testing.T) {
	g := testSession()
	good := nums(12, 8, 16, 80, 24, 0)
	if err := g.client(good); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][]byte{good[:len(good)-1], append(append([]byte(nil), good...), 0), nums(12, 8, 16, 80, 24, 2), nums(12, 8, 16, 0, 24, 0), nums(12, 8, 16, 65536, 65536, 0)} {
		if err := g.client(p); err == nil {
			t.Fatal("invalid resize accepted")
		}
		if !bytes.Equal(g.resize, good) || g.cols != 80 || g.rows != 24 {
			t.Fatal("invalid resize changed cached geometry")
		}
	}
}
