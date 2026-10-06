package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func recoverySession(t *testing.T) (*session, *[]Source, *time.Time, chan string) {
	t.Helper()
	g := testSession()
	g.ctx, g.cancel = context.WithCancel(context.Background())
	g.events = make(chan event, 2*MaxSources)
	now := time.Unix(1000, 0)
	g.clock = func() time.Time { return now }
	var catalog []Source
	g.catalog = func() []Source { return append([]Source(nil), catalog...) }
	opened := make(chan string, 4*MaxSources)
	t.Cleanup(func() {
		g.cancel()
		for _, b := range g.sources {
			g.remove(b)
		}
	})
	return g, &catalog, &now, opened
}

func recoverySource(id, generation string, opened chan<- string) Source {
	return Source{ID: id, Generation: generation, Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
		select {
		case opened <- id:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
}

func awaitRecoveryOpen(t *testing.T, opened <-chan string) {
	t.Helper()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("source was not opened")
	}
}

func TestRecoveryBackoffSurvivesStableCatalog(t *testing.T) {
	g, catalog, now, opened := recoverySession(t)
	*catalog = []Source{recoverySource("a", "publication", opened)}
	g.refresh()
	awaitRecoveryOpen(t, opened)
	for _, delay := range []time.Duration{1, 2, 4, 8, 16, 16} {
		g.fail(g.sources["a"], io.EOF)
		for range 20 {
			g.refresh()
			if g.sources["a"] != nil {
				t.Fatal("stable catalog reopened failed source before delay")
			}
		}
		*now = now.Add(delay*time.Second - time.Nanosecond)
		g.refresh()
		if g.sources["a"] != nil {
			t.Fatal("source reopened before retry deadline", delay)
		}
		*now = now.Add(time.Nanosecond)
		g.refresh()
		if g.sources["a"] == nil {
			t.Fatal("source did not recover at retry deadline", delay)
		}
		awaitRecoveryOpen(t, opened)
	}
}

func TestRecoveryGenerationAndVisibilityRetireRecords(t *testing.T) {
	g, catalog, now, opened := recoverySession(t)
	*catalog = []Source{recoverySource("a", "one", opened)}
	g.refresh()
	awaitRecoveryOpen(t, opened)
	old := g.sources["a"]
	g.fail(old, &UpstreamFailure{Code: "sizing_unavailable", Retryable: false})
	for range 100 {
		*now = now.Add(time.Hour)
		g.refresh()
		if g.sources["a"] != nil {
			t.Fatal("persistent failure reopened the same publication")
		}
	}
	(*catalog)[0].Generation = "two"
	g.refresh()
	awaitRecoveryOpen(t, opened)
	current := g.sources["a"]
	if current == nil || current == old || len(g.recovery) != 0 {
		t.Fatal("new publication inherited quarantine")
	}
	g.fail(old, io.EOF)
	if g.sources["a"] != current || len(g.recovery) != 0 {
		t.Fatal("late old-generation failure damaged replacement")
	}
	// A live replacement retires old requests before evaluating their expiry.
	g.pending["old-interest"] = current
	g.deadlines["old-interest"] = now.Add(-time.Second)
	current.initializeBy = now.Add(-time.Second)
	(*catalog)[0].Generation = "three"
	g.refresh()
	awaitRecoveryOpen(t, opened)
	if g.sources["a"].source.Generation != "three" || len(g.recovery) != 0 || len(g.pending) != 0 {
		t.Fatal("publication change counted old-generation deadlines as failures")
	}
	g.fail(g.sources["a"], io.EOF)
	*catalog = nil
	g.refresh()
	if len(g.sources) != 0 || len(g.recovery) != 0 {
		t.Fatal("unpublication retained failure state")
	}
	*catalog = []Source{recoverySource("a", "three", opened)}
	g.refresh()
	awaitRecoveryOpen(t, opened)
	if g.sources["a"] == nil {
		t.Fatal("explicit re-publication inherited invisible-source history")
	}
}

func TestRecoveryFailureStateIsBoundedByVisibleCapacity(t *testing.T) {
	g, catalog, _, opened := recoverySession(t)
	for round := range 3 {
		*catalog = nil
		for n := range MaxSources + 3 {
			*catalog = append(*catalog, recoverySource(fmt.Sprintf("%d-%02d", round, n), "one", opened))
		}
		g.refresh()
		for range MaxSources {
			awaitRecoveryOpen(t, opened)
		}
		if len(g.sources) != MaxSources {
			t.Fatal("upstreams exceeded visible capacity")
		}
		for _, b := range g.sources {
			g.fail(b, &UpstreamFailure{Code: "native_protocol"})
		}
		g.refresh()
		if len(g.recovery) != MaxSources || len(g.sources) != 0 {
			t.Fatal("failure capacity admitted hidden sources", len(g.recovery), len(g.sources))
		}
		*catalog = nil
		g.refresh()
		if len(g.recovery) != 0 {
			t.Fatal("catalog turnover accumulated failure history")
		}
	}
}

func TestRecoveryResetRequiresAcknowledgedRendering(t *testing.T) {
	for _, stage := range []string{"metadata", "ack-only", "frame-only", "short-render", "stable-render", "rejected-activation"} {
		t.Run(stage, func(t *testing.T) {
			g := testSession()
			now := time.Unix(1000, 0)
			g.clock = func() time.Time { return now }
			b := &backend{source: Source{ID: "a"}, stream: &memoryStream{}, cancel: func() {}, chunks: map[string][]byte{}, methods: map[string]bool{}, effects: map[uint64][]byte{}}
			g.sources["a"] = b
			welcome, _ := json.Marshal(fixtureWelcome())
			if err := g.server(b, control("endpoint.welcome.v1", welcome)); err != nil {
				t.Fatal(err)
			}
			if err := g.server(b, fixtureSnapshot("a")); err != nil {
				t.Fatal(err)
			}
			g.recovery = map[string]*recoveryRecord{"a": {attempts: 3}}
			failure := error(io.EOF)
			if stage != "metadata" {
				g.surfaceActive = true
				if err := g.setInterest(b, true); err != nil {
					t.Fatal(err)
				}
				id := readInterest(t, b, true)
				if stage != "ack-only" && stage != "rejected-activation" {
					if err := g.server(b, full("rendered")); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "rejected-activation" {
					failure = g.server(b, fixtureResponse(b.boot, id, `{"error":{"code":"unavailable"}}`, 1))
					if failure == nil {
						t.Fatal("rejected activation accepted")
					}
				} else if stage != "frame-only" {
					if err := g.server(b, responseFrame(b.boot, id)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if stage == "short-render" {
				now = now.Add(recoveryStableInterval - time.Nanosecond)
			} else {
				now = now.Add(2 * recoveryStableInterval)
			}
			g.fail(b, failure)
			want := uint8(4)
			if stage == "stable-render" {
				want = 1
			}
			if record := g.recovery["a"]; record.attempts != want || record.blocked {
				t.Fatalf("%s reset policy: got %+v, want attempt %d", stage, record, want)
			}
		})
	}
}

func TestRecoverySlowInitializationCannotBypassBackoff(t *testing.T) {
	g, catalog, now, opened := recoverySession(t)
	*catalog = []Source{recoverySource("a", "one", opened)}
	g.refresh()
	awaitRecoveryOpen(t, opened)
	for _, delay := range []time.Duration{1, 2, 4} {
		*now = now.Add(initializationTimeout)
		g.refresh()
		if g.sources["a"] != nil {
			t.Fatal("initialization timeout reopened in the same refresh")
		}
		*now = now.Add(delay*time.Second - time.Nanosecond)
		g.refresh()
		if g.sources["a"] != nil {
			t.Fatal("slow initialization skipped backoff")
		}
		*now = now.Add(time.Nanosecond)
		g.refresh()
		awaitRecoveryOpen(t, opened)
	}
}

func TestRecoveryFenceAckAndSendFailuresAreRecorded(t *testing.T) {
	for _, kind := range []string{"fence", "ack", "send", "ordinary"} {
		t.Run(kind, func(t *testing.T) {
			g, b := lifecycleSession()
			now := time.Unix(1000, 0)
			g.clock = func() time.Time { return now }
			switch kind {
			case "fence":
				g.fenceSource, g.fenceUntil = b, now
			case "ack", "ordinary":
				g.pending["expired"] = b
				g.deadlines["expired"] = now
				if kind == "ordinary" {
					g.requestIDs["expired"] = "consumer"
				}
			case "send":
				b.stream = &failedStream{}
				if err := g.send(b, []byte{17, 0}); err != nil {
					t.Fatal(err)
				}
			}
			g.refresh()
			if kind == "ordinary" {
				if len(g.recovery) != 0 || g.sources["a"] != b {
					t.Fatal("ordinary operation timeout penalized healthy source")
				}
			} else if g.sources["a"] != nil || g.recovery["a"] == nil || g.recovery["a"].attempts != 1 {
				t.Fatal("upstream lifecycle failure was not recorded", kind)
			}
		})
	}
}

func TestRecoveryProtocolFailureIsPersistent(t *testing.T) {
	g, catalog, _, opened := recoverySession(t)
	*catalog = []Source{recoverySource("a", "one", opened)}
	g.refresh()
	awaitRecoveryOpen(t, opened)
	b := g.sources["a"]
	err := g.server(b, control("shell.surface.unknown.v1", []byte("opaque")))
	var failure *UpstreamFailure
	if !errors.As(err, &failure) || failure.Retryable || failure.Code != "native_protocol" {
		t.Fatal("unnegotiated native codec was not persistent", err)
	}
	g.fail(b, err)
	g.refresh()
	if g.sources["a"] != nil || !g.recovery["a"].blocked {
		t.Fatal("protocol failure reopened unchanged publication")
	}
}

func TestRecoveryNeverReplaysOrAliasesOldOperations(t *testing.T) {
	g, old := lifecycleSession()
	if err := g.client(clientRequest(g, "reused", "workspace.rename", map[string]any{"workspace_id": "a/w1"})); err != nil {
		t.Fatal(err)
	}
	first := takeOperation(t, old)
	g.fail(old, io.EOF)
	if len(g.pending) != 0 || len(g.requestIDs) != 0 {
		t.Fatal("source retirement retained an operation for replay")
	}
	current := interestSource("a")
	current.methods["workspace.rename"] = true
	g.sources["a"], g.active = current, current
	if current.stream.(*memoryStream).Len() != 0 {
		t.Fatal("operation was replayed to replacement")
	}
	if err := g.client(clientRequest(g, "reused", "workspace.rename", map[string]any{"workspace_id": "a/w1"})); err != nil {
		t.Fatal(err)
	}
	next := takeOperation(t, current)
	if next.id == first.id {
		t.Fatal("replacement aliased a historical wire identity")
	}
	if err := g.server(current, responseFrame(current.boot, first.id)); err == nil {
		t.Fatal("historical response consumed replacement operation")
	}
	if g.pending[next.id] != current {
		t.Fatal("historical response changed live operation")
	}
	if err := g.server(current, responseFrame(current.boot, next.id)); err != nil {
		t.Fatal("replacement operation did not complete", err)
	}
}

func TestRecoveryPersistentAdapterFailureKeepsHealthySourceUsable(t *testing.T) {
	g, healthy := lifecycleSession()
	now := time.Unix(1000, 0)
	g.clock = func() time.Time { return now }
	failed := interestSource("b")
	failed.source.Generation = "one"
	g.sources["b"] = failed
	g.catalog = func() []Source { return []Source{healthy.source, failed.source} }
	g.fail(failed, &UpstreamFailure{Code: "sizing_unavailable", Retryable: false})
	for range 50 {
		now = now.Add(time.Minute)
		g.refresh()
	}
	if g.sources["b"] != nil || g.sources["a"] != healthy || g.active != healthy {
		t.Fatal("persistent adapter failure reopened or displaced healthy source")
	}
	if err := g.client(clientRequest(g, "healthy", "workspace.rename", map[string]any{"workspace_id": "a/w1"})); err != nil {
		t.Fatal(err)
	}
	r := takeOperation(t, healthy)
	if err := g.server(healthy, responseFrame(healthy.boot, r.id)); err != nil {
		t.Fatal("healthy operation failed after quarantine", err)
	}
	if len(g.pending) != 0 || len(g.requestIDs) != 0 {
		t.Fatal("healthy operation did not finish")
	}
}

func TestRecoveryProjectionIdentityIncludesPublicationGeneration(t *testing.T) {
	g := testSession()
	initialized := func(generation string) *backend {
		b := &backend{source: Source{ID: "a", Generation: generation}, stream: &memoryStream{}, cancel: func() {}, chunks: map[string][]byte{}, methods: map[string]bool{}, effects: map[uint64][]byte{}}
		g.sources["a"] = b
		welcome, _ := json.Marshal(fixtureWelcome())
		if err := g.server(b, control("endpoint.welcome.v1", welcome)); err != nil {
			t.Fatal(err)
		}
		if err := g.server(b, fixtureSnapshot("a")); err != nil {
			t.Fatal(err)
		}
		return b
	}
	old := initialized("one")
	g.remove(old)
	current := initialized("two")
	if current.prefix == old.prefix {
		t.Fatal("new publication reused historical projected IDs")
	}
	if err := g.client(clientRequest(g, "historical", "workspace.rename", map[string]any{"workspace_id": old.prefix + "w1"})); err != nil {
		t.Fatal(err)
	}
	if current.stream.(*memoryStream).Len() != 0 || len(g.pending) != 0 {
		t.Fatal("historical projected ID routed to replacement publication")
	}
	if err := g.client(clientRequest(g, "current", "workspace.rename", map[string]any{"workspace_id": current.prefix + "w1"})); err != nil {
		t.Fatal(err)
	}
	r := takeOperation(t, current)
	if r.method != "workspace.rename" {
		t.Fatal("new projected ID was not routable")
	}
}

func TestServePersistentFailureIsScopedToViewer(t *testing.T) {
	healthy := newFixtureOwner(t, "a", "", full("healthy"))
	opened := make(chan struct{}, 16)
	failed := Source{ID: "b", Generation: "one", Open: func(context.Context) (io.ReadWriteCloser, error) {
		opened <- struct{}{}
		return nil, &UpstreamFailure{Code: "sizing_unavailable", Retryable: false}
	}}
	first := newFixtureViewer(t, fixtureHello(), healthy.source, failed)
	firstLeg := nextFixtureLeg(t, healthy, 3*time.Second)
	first.snapshot(1, 3*time.Second)
	first.surface("healthy")
	first.forbidWithdrawal = true
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("first viewer did not attempt publication")
	}
	second := newFixtureViewer(t, fixtureHello(), healthy.source, failed)
	nextFixtureLeg(t, healthy, 3*time.Second)
	second.snapshot(1, 3*time.Second)
	second.surface("healthy")
	second.forbidWithdrawal = true
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("new viewer inherited another viewer's quarantine")
	}
	select {
	case <-opened:
		t.Fatal("unchanged persistent publication was retried")
	case <-time.After(2500 * time.Millisecond):
	}
	if err := firstLeg.write(patch(0, 1, 2, "healthy-after-two-viewers")); err != nil {
		t.Fatal(err)
	}
	first.surface("healthy-after-two-viewers")
	if healthy.count.Load() != 2 {
		t.Fatal("viewer-scoped failure recovery reconnected healthy streams")
	}
}
