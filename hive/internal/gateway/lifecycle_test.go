package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func lifecycleSession() (*session, *backend) {
	g := testSession()
	b := interestSource("a")
	b.methods["workspace.rename"] = true
	g.sources["a"], g.active = b, b
	g.catalog = func() []Source { return []Source{b.source} }
	return g, b
}

func takeOperation(t *testing.T, b *backend) fixtureRequest {
	t.Helper()
	p, err := Read(b.stream)
	if err != nil {
		t.Fatal(err)
	}
	d := decoder{b: p}
	if d.num() != 15 {
		t.Fatal("expected upstream operation")
	}
	r := fixtureRequest{boot: d.text()}
	var v map[string]any
	if json.Unmarshal([]byte(d.text()), &v) != nil {
		t.Fatal("invalid upstream operation")
	}
	r.id, r.method = stringVal(v["id"]), stringVal(v["method"])
	return r
}

func timeoutOperation(g *session, wireID string) {
	g.deadlines[wireID] = time.Now().Add(-time.Second)
	g.refresh()
}

func TestExpiredOperationsRemainBoundedAndDoNotBlockSurfaceInterest(t *testing.T) {
	g, b := lifecycleSession()
	var first fixtureRequest
	for i := range 64 {
		if err := g.client(clientRequest(g, "reused", "workspace.rename", map[string]any{"workspace_id": "a/w1", "name": fmt.Sprint(i)})); err != nil {
			t.Fatal(err)
		}
		r := takeOperation(t, b)
		if i == 0 {
			first = r
		}
		timeoutOperation(g, r.id)
		p, err := Read(g.out)
		if err != nil || fixtureResponseJSON(p)["id"] != "reused" || object(fixtureResponseJSON(p)["error"]) == nil {
			t.Fatal("timeout did not complete the consumer operation", err)
		}
	}
	if err := g.client(clientRequest(g, "over-capacity", "workspace.rename", map[string]any{"workspace_id": "a/w1", "name": "excess"})); err != nil {
		t.Fatal(err)
	}
	p, err := Read(g.out)
	if err != nil || fixtureResponseJSON(p)["id"] != "over-capacity" || object(fixtureResponseJSON(p)["error"]) == nil || b.stream.(*memoryStream).Len() != 0 {
		t.Fatal("expired capacity did not explicitly reject new operations", err)
	}
	// An expired ordinary request never steals the separately bounded interest
	// slots or disconnects the publisher when this viewer becomes visible.
	g.surfaceActive = true
	if err := g.publish(); err != nil {
		t.Fatal(err)
	}
	interest := takeOperation(t, b)
	if interest.method != "client_shell.surface.set" {
		t.Fatal("ordinary saturation prevented activation")
	}
	if err := g.server(b, responseFrame(b.boot, interest.id)); err != nil {
		t.Fatal(err)
	}
	before := g.out.(*memoryStream).Len()
	if err := g.server(b, fixtureResponse(first.boot, first.id, `{"id":"late","result":{"marker":"late"}}`, 1)); err != nil {
		t.Fatal(err)
	}
	if g.out.(*memoryStream).Len() != before {
		t.Fatal("expired final reached consumer")
	}
	if err := g.client(clientRequest(g, "reused", "workspace.rename", map[string]any{"workspace_id": "a/w1", "name": "after-final"})); err != nil {
		t.Fatal(err)
	}
	current := takeOperation(t, b)
	if current.id == first.id {
		t.Fatal("wire identity reused")
	}
	if g.sources["a"] != b {
		t.Fatal("capacity handling withdrew source")
	}
}

func TestExpiredResponseIdentityStillRejectsCrossSourceStaleBootAndDuplicate(t *testing.T) {
	g, b := lifecycleSession()
	if err := g.client(clientRequest(g, "r", "workspace.rename", map[string]any{"workspace_id": "a/w1"})); err != nil {
		t.Fatal(err)
	}
	r := takeOperation(t, b)
	timeoutOperation(g, r.id)
	other := interestSource("other")
	if err := g.server(other, responseFrame(other.boot, r.id)); err == nil {
		t.Fatal("cross-source expired response accepted")
	}
	if err := g.server(b, responseFrame("stale-boot", r.id)); err == nil {
		t.Fatal("stale-boot expired response accepted")
	}
	if err := g.server(b, responseFrame(b.boot, "never-issued")); err == nil {
		t.Fatal("unsolicited response accepted")
	}
	if err := g.server(b, responseFrame(b.boot, r.id)); err != nil {
		t.Fatal(err)
	}
	if err := g.server(b, responseFrame(b.boot, r.id)); err == nil {
		t.Fatal("duplicate expired final accepted")
	}
}

func TestUnnegotiatedRenderingControlsDoNotChangeCompleteBaseline(t *testing.T) {
	for _, kind := range []string{"endpoint.surface-reuse.v1", "endpoint.surface-delta.v1", "endpoint.surface-scroll.v1", "endpoint.surface-future.v2", "shell.surface.v2", "shell.snapshot.v2"} {
		t.Run(kind, func(t *testing.T) {
			g := testSession()
			b := &backend{boot: "bee-boot", welcomed: true, snapshot: map[string]any{"revision": float64(7)}}
			if err := b.frame.update(full("baseline")); err != nil {
				t.Fatal(err)
			}
			baseline := b.frame.bytes()
			if err := g.server(b, control(kind, []byte(`{"surface":"unsupported"}`))); err == nil {
				t.Fatal("unnegotiated rendering codec silently accepted")
			}
			if !bytes.Equal(baseline, b.frame.bytes()) {
				t.Fatal("unnegotiated codec polluted complete baseline")
			}
		})
	}
}

func TestWelcomeRejectsSelectedUnknownCodecAndGeneration(t *testing.T) {
	for _, field := range []string{"generation", "snapshot_codec", "surface_codec", "input_codec", "blob_codec"} {
		t.Run(field, func(t *testing.T) {
			g := testSession()
			b := &backend{methods: map[string]bool{}}
			v := fixtureWelcome()
			if field == "generation" {
				v[field] = 2
			} else {
				v[field] = "unknown.codec.v2"
			}
			data, _ := json.Marshal(v)
			if err := g.server(b, control("endpoint.welcome.v1", data)); err == nil {
				t.Fatal("breaking selected codec was accepted")
			}
		})
	}
}

func TestPatchRejectsCrossBootProjectionAndNonSuccessorRevisions(t *testing.T) {
	for _, change := range []func([]byte) []byte{
		func(p []byte) []byte { return bytes.Replace(p, str("bee-boot"), str("other-boot"), 1) },
		func(p []byte) []byte {
			d := decoder{b: p}
			d.num()
			d.text()
			d.replaceNum(8)
			return append(d.out, p[d.p:]...)
		},
		func(p []byte) []byte {
			d := decoder{b: p}
			d.num()
			d.text()
			d.nums(2)
			d.replaceNum(99)
			return append(d.out, p[d.p:]...)
		},
	} {
		var state completeSurface
		if err := state.update(full("base")); err != nil {
			t.Fatal(err)
		}
		before := state.bytes()
		if err := state.update(change(patch(0, 1, 2, "wrong"))); err == nil {
			t.Fatal("patch crossed a baseline identity/revision boundary")
		}
		if !bytes.Equal(before, state.bytes()) {
			t.Fatal("rejected patch changed baseline")
		}
	}
}

func TestResizeRejectsInvalidFrozenGeometry(t *testing.T) {
	for _, dimensions := range [][]uint64{
		{1 << 32, 16, 1, 1},
		{8, 1 << 32, 1, 1},
		{8, 16, 0, 1},
		{8, 16, 65536, 1},
		{8, 16, 257, 256},
	} {
		g := testSession()
		p := append([]byte{12}, nums(dimensions...)...)
		p = append(p, 0)
		if err := g.client(p); err == nil {
			t.Fatal("invalid resize geometry accepted", dimensions)
		}
	}
}

func TestSaturatedInternalTransitionsDeferActivationAndFenceWithoutDisconnect(t *testing.T) {
	g := testSession()
	a, b := interestSource("a"), interestSource("b")
	g.sources["a"], g.sources["b"] = a, b
	g.active, g.surfaceActive, a.surfaceActive = b, true, true
	g.fence, g.fencePending = "after-activation", true
	for i := range 16 {
		id := fmt.Sprintf("%s%d", interestPrefix, i+1)
		g.pending[id] = a
		g.deadlines[id] = time.Now().Add(time.Minute)
	}
	g.interestSequence = 16
	if err := g.publish(); err != nil {
		t.Fatal("internal capacity closed viewer", err)
	}
	if a.stream.(*memoryStream).Len() != 0 || b.stream.(*memoryStream).Len() != 0 {
		t.Fatal("activation or fence bypassed a saturated release transition")
	}
	if err := g.server(a, responseFrame(a.boot, interestPrefix+"1")); err != nil {
		t.Fatal(err)
	}
	readInterest(t, a, false)
	if b.stream.(*memoryStream).Len() != 0 {
		t.Fatal("replacement acquired interest without a slot")
	}
	if err := g.server(a, responseFrame(a.boot, interestPrefix+"2")); err != nil {
		t.Fatal(err)
	}
	readInterest(t, b, true)
	p, err := Read(b.stream)
	if err != nil {
		t.Fatal(err)
	}
	d := decoder{b: p}
	if d.num() != 20 || d.text() != "endpoint.presentation.sync.v1" || d.text() != "after-activation" {
		t.Fatal("deferred fence did not follow activation")
	}
	if g.sources["a"] != a || g.sources["b"] != b {
		t.Fatal("capacity withdrew a healthy source")
	}
}

func TestConsumerRequestIDsCannotImpersonateInternalAcknowledgements(t *testing.T) {
	for _, consumerID := range []string{interestPrefix + "1", requestPrefix + "1", "hive-interest:1", "bee-sizing:1"} {
		t.Run(consumerID, func(t *testing.T) {
			g, b := lifecycleSession()
			internalID := interestPrefix + "1"
			g.pending[internalID] = b
			g.deadlines[internalID] = time.Now().Add(time.Minute)
			g.interestSequence = 1
			if err := g.client(clientRequest(g, consumerID, "workspace.rename", map[string]any{"workspace_id": "a/w1", "name": "consumer"})); err != nil {
				t.Fatal("ordinary consumer ID was treated as a wire control", err)
			}
			r := takeOperation(t, b)
			if err := g.server(b, fixtureResponse(r.boot, r.id, `{"result":{"marker":"consumer"}}`, 1)); err != nil {
				t.Fatal(err)
			}
			p, err := Read(g.out)
			v := fixtureResponseJSON(p)
			if err != nil || v["id"] != consumerID || object(v["result"])["marker"] != "consumer" {
				t.Fatal("ordinary response was swallowed as an internal acknowledgement", err, v)
			}
			before := g.out.(*memoryStream).Len()
			if err := g.server(b, responseFrame(b.boot, internalID)); err != nil {
				t.Fatal("consumer alias consumed a real internal acknowledgement", err)
			}
			if g.out.(*memoryStream).Len() != before {
				t.Fatal("internal acknowledgement leaked to consumer")
			}
		})
	}
}
