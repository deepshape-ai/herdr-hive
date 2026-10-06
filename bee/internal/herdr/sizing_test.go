package herdr

import (
	"bytes"
	"context"
	encodingbinary "encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func testSnapshot(t *testing.T, tab string) shellSnapshot {
	t.Helper()
	var s shellSnapshot
	if err := json.Unmarshal([]byte(`{"focused_tab_id":"`+tab+`","panes":[{"pane_id":"w1:p1","tab_id":"w1:t1"},{"pane_id":"w1:p2","tab_id":"w1:t2"}]}`), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testSizeLock() (*sizeLock, chan struct{}) {
	done := make(chan struct{})
	var once sync.Once
	return &sizeLock{done: done, cancel: func() { once.Do(func() { close(done) }) }}, done
}

func serveSizingPaneList(listener net.Listener, panes ...string) <-chan string {
	methods := make(chan string, 16)
	catalog := make([]map[string]string, 0, len(panes))
	for _, pane := range panes {
		catalog = append(catalog, map[string]string{"pane_id": pane})
	}
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Method string `json:"method"`
			}
			if json.NewDecoder(c).Decode(&request) == nil {
				methods <- request.Method
				if request.Method == "pane.list" {
					json.NewEncoder(c).Encode(map[string]any{"result": map[string]any{"panes": catalog}})
				} else {
					json.NewEncoder(c).Encode(map[string]any{"error": map[string]string{"code": "unexpected_fixture_method"}})
				}
			}
			c.Close()
		}
	}()
	return methods
}

func awaitSizingRelease(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(sizingIdleGrace + time.Second):
		t.Fatal("zero-view controller was not retired")
	}
}

// This test uses only pre-existing fields and methods so it can be copied onto
// the unfixed baseline: its first assertion fails on immediate last-view close.
func TestSizingRapidLastViewerReconnectRetainsExactController(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once
	lock := &sizeLock{done: done, cancel: func() { once.Do(func() { close(done) }) }}
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
	t.Cleanup(func() { a.close(); b.close(); lock.cancel() })
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	a.close()
	select {
	case <-done:
		t.Fatal("last-view disconnect tore down the controller before reconnect")
	default:
	}
	if err := b.reconcile(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	same := s.locks["w1:p1"] == lock && lock.refs == 1
	s.mu.Unlock()
	if !same {
		t.Fatal("reconnect did not reuse the exact live geometry controller")
	}
	select {
	case <-done:
		t.Fatal("reconnect reused a dead controller")
	default:
	}
	b.close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("controller did not restore local sizing after all viewers left")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.locks) != 0 {
		t.Fatal("expired controller retained")
	}
}

func TestSizingReferencesSurviveFirstViewerDeparture(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once
	lock := &sizeLock{done: done, cancel: func() { once.Do(func() { close(done) }) }}
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
	t.Cleanup(s.close)
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
	for _, v := range []*sizeView{a, b} {
		if err := v.reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	a.close()
	select {
	case <-done:
		t.Fatal("first viewer released the shared controller")
	default:
	}
	if lock.refs != 1 {
		t.Fatal(lock.refs)
	}
	b.close()
	awaitSizingRelease(t, done)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.locks) != 0 {
		t.Fatal("retained released lock")
	}
}
func TestHiddenViewNeverAcquiresSizing(t *testing.T) {
	v := &sizeView{sizing: &Sizing{locks: map[string]*sizeLock{}}, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	if err := v.reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(v.held) != 0 {
		t.Fatal("background view acquired a lock")
	}
}
func TestDeadControllerFailsActiveViewerButStillReleases(t *testing.T) {
	b, listener := apiFixture(t)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var request map[string]any
		if json.NewDecoder(c).Decode(&request) == nil {
			json.NewEncoder(c).Encode(map[string]any{"result": map[string]any{"panes": []map[string]string{{"pane_id": "w1:p1"}}}})
		}
	}()
	done := make(chan struct{})
	close(done)
	l := &sizeLock{refs: 1, done: done, cancel: func() {}}
	b.sizing.locks["w1:p1"] = l
	v := &sizeView{ctx: context.Background(), sizing: b.sizing, active: true, held: map[string]bool{"w1:p1": true}, snapshot: testSnapshot(t, "w1:t1")}
	var failure *StreamFailure
	if err := v.reconcile(); !errors.As(err, &failure) || failure.Code != "sizing_unavailable" || failure.Retryable {
		t.Fatal("dead live-pane controller was not classified", err)
	}
	v.close()
	if len(v.sizing.locks) != 0 {
		t.Fatal("dead controller retained")
	}
}
func TestSizingRelayPreservesOpaqueFramesAndBoundsControl(t *testing.T) {
	for _, tag := range []byte{1, 2, 12, 13, 19, 20} {
		data := append([]byte{tag}, bytes.Repeat([]byte{42}, 4096)...)
		var wire, out bytes.Buffer
		if err := writeSizingFrame(&wire, data); err != nil {
			t.Fatal(err)
		}
		inspected := false
		err := relaySizingFrame(&out, &wire, func(p []byte) ([]byte, error) {
			inspected = true
			if !bytes.Equal(p, data) {
				t.Fatal("changed payload")
			}
			return p, nil
		}, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := readSizingFrame(&out)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatal("opaque frame corrupted", err)
		}
		if inspected != (tag == 20) {
			t.Fatal("wrong inspected frame", tag)
		}
	}
	var h [5]byte
	encodingbinary.LittleEndian.PutUint32(h[:4], 3<<20)
	h[4] = 20
	if relaySizingFrame(io.Discard, bytes.NewReader(h[:]), func(p []byte) ([]byte, error) { t.Fatal("oversized control inspected"); return nil, nil }, true) == nil {
		t.Fatal("unbounded control frame")
	}
}
func TestSizingRelayCancellationUnblocksBothDirections(t *testing.T) {
	for _, output := range []bool{false, true} {
		t.Run(map[bool]string{false: "input", true: "output"}[output], func(t *testing.T) {
			local, server := net.Pipe()
			defer server.Close()
			remote, viewer := net.Pipe()
			defer viewer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := Bound{sizing: &Sizing{locks: map[string]*sizeLock{}}}
			done := make(chan error, 1)
			go func() { done <- b.Relay(ctx, remote, local, nil) }()
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				if output {
					writeSizingFrame(server, []byte{13, 1, 2, 3})
				} else {
					writeSizingFrame(viewer, []byte{12, 1, 2, 3})
				}
			}()
			// The other side intentionally never reads: output/input backpressure must
			// not prevent publication shutdown, even with a partially forwarded frame.
			time.Sleep(20 * time.Millisecond)
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("relay cancellation stuck")
			}
			select {
			case <-sent:
			case <-time.After(time.Second):
				t.Fatal("writer leaked")
			}
		})
	}
}
func TestSizingDecoderRejectsTruncatedAndHugeStrings(t *testing.T) {
	for _, p := range [][]byte{{251}, {252, 1}, {253, 255, 255, 255, 255, 255, 255, 255, 255}, {255}} {
		d := wireDecoder{data: p}
		d.text()
		if d.err == nil {
			t.Fatal("malformed string accepted", p)
		}
	}
}

func TestOldSnapshotCannotReleasePendingDestination(t *testing.T) {
	s := &Sizing{locks: map[string]*sizeLock{}}
	t.Cleanup(s.close)
	ends := map[string]chan struct{}{}
	for _, pane := range []string{"w1:p1", "w1:p2"} {
		done := make(chan struct{})
		ends[pane] = done
		var once sync.Once
		s.locks[pane] = &sizeLock{done: done, cancel: func() { once.Do(func() { close(done) }) }}
	}
	snap := testSnapshot(t, "w1:t1")
	snap.Boot = "boot"
	snap.Revision = 1
	v := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: snap}
	if err := v.reconcile(); err != nil {
		t.Fatal(err)
	}
	request := append(append([]byte{15}, wireText("boot")...), wireText(`{"id":"focus-b","method":"tab.focus","params":{"tab_id":"w1:t2"}}`)...)
	if _, err := v.clientFrame(request); err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(snap)
	if _, err := v.serverFrame(wireControl("shell.snapshot.v1", string(old))); err != nil {
		t.Fatal(err)
	}
	for pane, done := range ends {
		select {
		case <-done:
			t.Fatal("old snapshot released", pane)
		default:
		}
	}
	ack := append(append([]byte{18}, wireText("boot")...), wireText("focus-b")...)
	ack = append(ack, 1)
	ack = append(ack, wireText(`{"id":"focus-b","result":{}}`)...)
	if _, err := v.serverFrame(ack); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ends["w1:p1"]:
		t.Fatal("released before matching snapshot")
	default:
	}
	snap.Tab = "w1:t2"
	snap.Revision = 2
	next, _ := json.Marshal(snap)
	if _, err := v.serverFrame(wireControl("shell.snapshot.v1", string(next))); err != nil {
		t.Fatal(err)
	}
	if v.held["w1:p1"] || !v.held["w1:p2"] {
		t.Fatal("confirmed navigation retained the old view reference")
	}
	awaitSizingRelease(t, ends["w1:p1"])
	if len(v.pending) != 0 {
		t.Fatal("confirmed navigation retained")
	}
	v.close()
}
func TestInitialHandshakePinsBeforeActivation(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": {done: done, cancel: func() { once.Do(func() { close(done) }) }}}}
	t.Cleanup(s.close)
	v := &sizeView{sizing: s, held: map[string]bool{}}
	p, err := v.clientFrame(wireControl("endpoint.hello.v1", `{"generation":1,"surface_active":true}`))
	if err != nil {
		t.Fatal(err)
	}
	d := wireDecoder{data: p}
	d.number()
	d.text()
	var h map[string]any
	json.Unmarshal([]byte(d.text()), &h)
	if h["surface_active"] != false {
		t.Fatal("hello acquired geometry")
	}
	activated := false
	v.sendUp = func(p []byte) error {
		activated = true
		if !v.held["w1:p1"] {
			t.Fatal("activation preceded lock")
		}
		return nil
	}
	snap := testSnapshot(t, "w1:t1")
	snap.Boot = "boot"
	snap.Revision = 1
	b, _ := json.Marshal(snap)
	if _, err := v.serverFrame(wireControl("shell.snapshot.v1", string(b))); err != nil {
		t.Fatal(err)
	}
	if !activated {
		t.Fatal("initial surface never activated")
	}
	v.close()
}

func readSizingFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := encodingbinary.LittleEndian.Uint32(h[:])
	if n == 0 || n > maxSizingFrame {
		return nil, errSizingWire
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

func TestProjectionBarrierSettlesCoalescedNavigation(t *testing.T) {
	v := &sizeView{snapshot: shellSnapshot{Boot: "boot", Tab: "w1:t1", Revision: 4}, sizing: &Sizing{locks: map[string]*sizeLock{}}, held: map[string]bool{}, pending: map[string]*navigation{"focus-b": {tab: "w1:t2", seq: 1, ack: true, since: time.Now()}}}
	// Herdr accepted focus B, but an intervening close/focus leaves only final A
	// visible. Its explicit projection floor proves the navigation has completed.
	ack := append(append([]byte{18}, wireText("boot")...), wireText("bee-sizing:nav:1")...)
	ack = append(ack, 1)
	ack = append(ack, wireText(`{"result":{"projection_revision":5}}`)...)
	if _, err := v.serverFrame(ack); err != nil {
		t.Fatal(err)
	}
	if len(v.pending) != 1 {
		t.Fatal("barrier released before fresh snapshot")
	}
	snap := v.snapshot
	snap.Revision = 6
	b, _ := json.Marshal(snap)
	if _, err := v.serverFrame(wireControl("shell.snapshot.v1", string(b))); err != nil {
		t.Fatal(err)
	}
	if len(v.pending) != 0 {
		t.Fatal("coalesced navigation would time out")
	}
}

func TestSizingHideShowRetainsControllerUntilConfirmedHideExpires(t *testing.T) {
	lock, done := testSizeLock()
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
	t.Cleanup(s.close)
	snap := testSnapshot(t, "w1:t1")
	snap.Boot, snap.Revision = "boot", 1
	v := &sizeView{sizing: s, active: true, requestedActive: true, held: map[string]bool{}, snapshot: snap}
	if err := v.reconcile(); err != nil {
		t.Fatal(err)
	}
	request := append(append([]byte{15}, wireText("boot")...), wireText(`{"id":"hide","method":"client_shell.surface.set","params":{"active":false}}`)...)
	if _, err := v.clientFrame(request); err != nil {
		t.Fatal(err)
	}
	if !v.held["w1:p1"] {
		t.Fatal("unacknowledged hide released sizing")
	}
	ack := append(append([]byte{18}, wireText("boot")...), wireText("hide")...)
	ack = append(ack, 1)
	ack = append(ack, wireText(`{"result":{}}`)...)
	if _, err := v.serverFrame(ack); err != nil {
		t.Fatal(err)
	}
	if v.held["w1:p1"] {
		t.Fatal("confirmed hide retained the viewer reference")
	}
	select {
	case <-done:
		t.Fatal("hide tore down sizing before a rapid show")
	default:
	}
	show := append(append([]byte{15}, wireText("boot")...), wireText(`{"id":"show","method":"client_shell.surface.set","params":{"active":true}}`)...)
	if _, err := v.clientFrame(show); err != nil {
		t.Fatal(err)
	}
	if s.locks["w1:p1"] != lock || lock.refs != 1 {
		t.Fatal("show replaced the controller or lost its reference")
	}
	v.close()
	awaitSizingRelease(t, done)
}

func TestSizingStaleExpirationCannotCancelReacquiredOrReparkedController(t *testing.T) {
	for _, repark := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "idle-again"}[repark], func(t *testing.T) {
			lock, done := testSizeLock()
			s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
			t.Cleanup(s.close)
			a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
			if err := a.reconcile(); err != nil {
				t.Fatal(err)
			}
			a.close()
			s.mu.Lock()
			expiredEpoch := lock.epoch
			s.mu.Unlock()
			// The old timer has fired, but its callback has not entered the
			// registry yet. A reconnect may win precisely this ordering.
			enter, finished := make(chan struct{}), make(chan struct{})
			go func() {
				<-enter
				s.expire("w1:p1", lock, expiredEpoch)
				close(finished)
			}()
			b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
			if err := b.reconcile(); err != nil {
				close(enter)
				t.Fatal(err)
			}
			if repark {
				b.close()
			}
			close(enter)
			<-finished
			select {
			case <-done:
				t.Fatal("stale expiration cancelled a newer lifetime")
			default:
			}
			s.mu.Lock()
			same := s.locks["w1:p1"] == lock
			s.mu.Unlock()
			if !same {
				t.Fatal("stale expiration removed the reacquired controller")
			}
			b.close()
			awaitSizingRelease(t, done)
		})
	}
}

func TestSizingRemovedPaneStopsImmediatelyWithoutAffectingOtherTab(t *testing.T) {
	bound, listener := apiFixture(t)
	serveSizingPaneList(listener, "w1:p2")
	first, firstDone := testSizeLock()
	second, secondDone := testSizeLock()
	s := bound.sizing
	s.locks["w1:p1"], s.locks["w1:p2"] = first, second
	snap := testSnapshot(t, "w1:t1")
	snap.Boot, snap.Revision = "boot", 1
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: snap}
	snap.Tab = "w1:t2"
	b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: snap}
	for _, v := range []*sizeView{a, b} {
		if err := v.reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	snap.Panes = snap.Panes[1:]
	snap.Revision = 2
	data, _ := json.Marshal(snap)
	if _, err := b.serverFrame(wireControl("shell.snapshot.v1", string(data))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	default:
		t.Fatal("removed pane waited for idle grace despite having no PTY")
	}
	select {
	case <-secondDone:
		t.Fatal("pane removal stopped another viewer's independent tab")
	default:
	}
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(a.held) != 0 || second.refs != 1 {
		t.Fatal("stale viewer reacquired a removed pane or changed another reference")
	}
	a.close()
	b.close()
}

func TestSizingDeadIdleHelperIsRemovedAndNeverReused(t *testing.T) {
	bound, listener := apiFixture(t)
	serveSizingPaneList(listener, "w1:p1")
	lock, done := testSizeLock()
	s := bound.sizing
	s.locks["w1:p1"] = lock
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	a.close()
	watched := make(chan struct{})
	go func() { s.watch("w1:p1", lock); close(watched) }()
	lock.cancel() // Native helper exits independently while no viewer is present.
	select {
	case <-watched:
	case <-time.After(time.Second):
		t.Fatal("dead idle helper was retained until its grace timer")
	}
	s.mu.Lock()
	retained := s.locks["w1:p1"] != nil
	s.mu.Unlock()
	if retained {
		t.Fatal("dead helper retained in the registry")
	}
	b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
	var failure *StreamFailure
	if err := b.reconcile(); !errors.As(err, &failure) || failure.Code != "sizing_unavailable" {
		t.Fatal("new viewer accepted a dead controller or retried it immediately", err)
	}
	if len(b.held) != 0 {
		t.Fatal("failed viewer owns dead sizing")
	}
	select {
	case <-done:
	default:
		t.Fatal("helper fixture did not exit")
	}
}

func TestSizingIdleBudgetIsReclaimedBeforeRejectingLivePane(t *testing.T) {
	t.Cleanup(func() {
		for len(sizeSlots) > 0 {
			<-sizeSlots
		}
	})
	for range cap(sizeSlots) {
		if err := reserveSizeSlot(); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	var once sync.Once
	lock := &sizeLock{refs: 1, done: done, cancel: func() {
		once.Do(func() { <-sizeSlots; close(done) })
	}}
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
	t.Cleanup(s.close)
	v := &sizeView{sizing: s, held: map[string]bool{"w1:p1": true}}
	v.close()
	if err := reserveSizeSlot(); err != nil {
		t.Fatal("idle helper exhausted live process-wide capacity", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("capacity admission did not retire the idle helper")
	}
	if len(sizeSlots) != cap(sizeSlots) {
		t.Fatal("process-wide capacity changed during reclamation")
	}
	var failure *StreamFailure
	if err := reserveSizeSlot(); !errors.As(err, &failure) || failure.Code != "sizing_limit" || !failure.Retryable {
		t.Fatal("live capacity limit was not preserved", err)
	}
}

func TestSizingFreshViewerCanAcquireAfterEarlierPaneFailure(t *testing.T) {
	b, listener := apiFixture(t)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Method string `json:"method"`
			}
			if json.NewDecoder(c).Decode(&request) == nil {
				var result any
				switch request.Method {
				case "pane.get":
					result = map[string]any{"pane": map[string]string{"terminal_id": ""}}
				case "pane.process_info":
					result = map[string]any{"process_info": map[string]int{"shell_pid": 0}}
				case "pane.list":
					result = map[string]any{"panes": []map[string]string{{"pane_id": "w1:p1"}, {"pane_id": "w1:p2"}}}
				}
				json.NewEncoder(c).Encode(map[string]any{"result": result})
			}
			c.Close()
		}
	}()
	healthy, healthyDone := testSizeLock()
	b.sizing.locks["w1:p1"] = healthy
	snap := testSnapshot(t, "w1:t1")
	snap.Panes[1].Tab = "w1:t1"
	failed := &sizeView{sizing: b.sizing, active: true, held: map[string]bool{}, snapshot: snap}
	var failure *StreamFailure
	if err := failed.reconcile(); !errors.As(err, &failure) || failure.Code != "sizing_unavailable" {
		t.Fatal("missing PTY did not reject acquisition", err)
	}
	if len(failed.held) != 0 || healthy.refs != 0 {
		t.Fatal("failed multi-pane setup acquired unrelated healthy resources")
	}
	failed.close()
	// The native controller is now available. A fresh view must use current
	// resources rather than inherit another view's previous adapter failure.
	recovered, recoveredDone := testSizeLock()
	b.sizing.mu.Lock()
	b.sizing.locks["w1:p2"] = recovered
	b.sizing.mu.Unlock()
	fresh := &sizeView{sizing: b.sizing, active: true, held: map[string]bool{}, snapshot: snap}
	if err := fresh.reconcile(); err != nil {
		t.Fatal("previous viewer failure rejected a recovered fresh view", err)
	}
	if !fresh.held["w1:p1"] || !fresh.held["w1:p2"] || healthy.refs != 1 || recovered.refs != 1 {
		t.Fatal("fresh view did not acquire both current controllers")
	}
	for _, done := range []<-chan struct{}{healthyDone, recoveredDone} {
		select {
		case <-done:
			t.Fatal("previous failure retired a current controller")
		default:
		}
	}
	fresh.close()
}

type closeObservedSizingConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *closeObservedSizingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestSizingRelayReportsTypedFailureBeforeClosingStream(t *testing.T) {
	local, server := net.Pipe()
	defer server.Close()
	remote, viewer := net.Pipe()
	defer viewer.Close()
	observed := &closeObservedSizingConn{Conn: remote, closed: make(chan struct{})}
	b := Bound{sizing: &Sizing{locks: map[string]*sizeLock{}}}
	result := make(chan error, 1)
	reports := 0
	go func() {
		result <- b.Relay(context.Background(), observed, local, func(failure *StreamFailure) {
			reports++
			if failure.Code != "native_protocol" || failure.Retryable {
				t.Error("unsupported generation did not report a persistent protocol failure")
			}
			select {
			case <-observed.closed:
				t.Error("failure was reported after remote channel closure")
			default:
			}
		})
	}()
	if err := writeSizingFrame(viewer, wireControl("endpoint.hello.v1", `{"generation":2}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		var failure *StreamFailure
		if !errors.As(err, &failure) {
			t.Fatal("relay lost typed protocol failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failed relay did not stop")
	}
	if reports != 1 {
		t.Fatal("failure sideband was not reported exactly once", reports)
	}
	select {
	case <-observed.closed:
	default:
		t.Fatal("failed relay left the remote stream open")
	}
}

func TestSizingRetiredHelperCannotRemoveReplacementOrItsViewerReference(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "dead-active", true: "expiry-wins"}[expired], func(t *testing.T) {
			old, _ := testSizeLock()
			s := &Sizing{locks: map[string]*sizeLock{"w1:p1": old}}
			t.Cleanup(s.close)
			a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
			if err := a.reconcile(); err != nil {
				t.Fatal(err)
			}
			if expired {
				a.close()
				s.mu.Lock()
				epoch := old.epoch
				s.mu.Unlock()
				s.expire("w1:p1", old, epoch)
			} else {
				old.cancel()
				s.mu.Lock()
				s.stopLocked("w1:p1", old)
				s.mu.Unlock()
			}
			replacement, replacementDone := testSizeLock()
			s.mu.Lock()
			s.locks["w1:p1"] = replacement
			s.mu.Unlock()
			b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
			if err := b.reconcile(); err != nil {
				t.Fatal(err)
			}
			a.close()
			s.expire("w1:p1", old, old.epoch)
			s.mu.Lock()
			intact := s.locks["w1:p1"] == replacement && replacement.refs == 1
			s.mu.Unlock()
			if !intact {
				t.Fatal("old viewer or expiration released the replacement's reference")
			}
			select {
			case <-replacementDone:
				t.Fatal("retired helper cancelled the replacement")
			default:
			}
			b.close()
		})
	}
}

func TestSizingDeadRemovedPaneDoesNotFailHealthyPublication(t *testing.T) {
	b, listener := apiFixture(t)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var request map[string]any
		if json.NewDecoder(c).Decode(&request) == nil {
			json.NewEncoder(c).Encode(map[string]any{"result": map[string]any{"panes": []any{}}})
		}
	}()
	lock, _ := testSizeLock()
	lock.cancel()
	lock.refs = 1
	b.sizing.locks["w1:p1"] = lock
	v := &sizeView{sizing: b.sizing, ctx: context.Background(), active: true, held: map[string]bool{"w1:p1": true}, snapshot: testSnapshot(t, "w1:t1")}
	if err := v.reconcile(); err != nil {
		t.Fatal("removed PTY was treated as a persistent acquisition failure", err)
	}
	if len(v.held) != 0 || len(b.sizing.locks) != 0 {
		t.Fatal("removed dead pane retained controller references")
	}
	v.close()
}

func TestSizingNavigationTimeoutIsRetryableWithoutReleasingOtherViewer(t *testing.T) {
	lock, done := testSizeLock()
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
	t.Cleanup(s.close)
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
	b := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: a.snapshot}
	for _, v := range []*sizeView{a, b} {
		if err := v.reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	a.pending = map[string]*navigation{"stalled": {tab: "w1:t2", since: time.Now().Add(-11 * time.Second)}}
	var failure *StreamFailure
	if err := a.reconcile(); !errors.As(err, &failure) || failure.Code != "navigation_timeout" || !failure.Retryable {
		t.Fatal("stalled barrier was not a retryable navigation failure", err)
	}
	a.close()
	select {
	case <-done:
		t.Fatal("failed navigation released another viewer's controller")
	default:
	}
	if lock.refs != 1 {
		t.Fatal("failed viewer leaked or released the healthy viewer reference")
	}
	b.close()
}

func TestSizingLocalIOFailureRemainsTransientForFreshViewer(t *testing.T) {
	b, listener := apiFixture(t)
	go func() {
		for range 4 {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Method string `json:"method"`
			}
			if json.NewDecoder(c).Decode(&request) == nil {
				if request.Method == "pane.list" {
					json.NewEncoder(c).Encode(map[string]any{"result": map[string]any{"panes": []map[string]string{{"pane_id": "w1:p1"}}}})
				}
				// A geometry query loses its local connection before a response.
			}
			c.Close()
		}
	}()
	for range 2 {
		v := &sizeView{sizing: b.sizing, ctx: context.Background(), active: true, held: map[string]bool{}, snapshot: testSnapshot(t, "w1:t1")}
		err := v.reconcile()
		var failure *StreamFailure
		if !errors.Is(err, io.EOF) || errors.As(err, &failure) {
			t.Fatal("local I/O was made into a persistent stream failure", err)
		}
		v.close()
	}
}

func TestSizingFreshLowRevisionPinsNewPaneAndStaleViewerCannotEvictIt(t *testing.T) {
	bound, listener := apiFixture(t)
	serveSizingPaneList(listener, "w1:p2")
	old, oldDone := testSizeLock()
	s := bound.sizing
	s.locks["w1:p1"] = old
	prior := testSnapshot(t, "w1:t1")
	prior.Boot, prior.Revision = "boot", 100
	prior.Panes = prior.Panes[:1]
	a := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: prior}
	if err := a.reconcile(); err != nil {
		t.Fatal(err)
	}
	a.close()
	next, nextDone := testSizeLock()
	s.mu.Lock()
	s.locks["w1:p2"] = next
	s.mu.Unlock()
	freshSnapshot := testSnapshot(t, "w1:t2")
	freshSnapshot.Boot, freshSnapshot.Revision = "boot", 1
	freshSnapshot.Panes = freshSnapshot.Panes[1:]
	fresh := &sizeView{sizing: s, active: true, held: map[string]bool{}}
	data, _ := json.Marshal(freshSnapshot)
	if _, err := fresh.serverFrame(wireControl("shell.snapshot.v1", string(data))); err != nil {
		t.Fatal(err)
	}
	if !fresh.held["w1:p2"] || next.refs != 1 {
		t.Fatal("old viewer's projection revision suppressed fresh pane sizing")
	}
	select {
	case <-oldDone:
	default:
		t.Fatal("authoritatively removed old pane retained its idle controller")
	}
	// This newly connected viewer has a higher, but stale projection. Neither
	// its revision nor its omission of p2 grants authority to remove p2.
	prior.Revision = 200
	stale := &sizeView{sizing: s, active: true, held: map[string]bool{}}
	data, _ = json.Marshal(prior)
	if _, err := stale.serverFrame(wireControl("shell.snapshot.v1", string(data))); err != nil {
		t.Fatal(err)
	}
	if len(stale.held) != 0 || next.refs != 1 {
		t.Fatal("stale viewer acquired a removed pane or released the fresh viewer")
	}
	select {
	case <-nextDone:
		t.Fatal("stale viewer evicted a healthy pane using incomparable revisions")
	default:
	}
	stale.close()
	fresh.close()
}

func TestSizingRemovedIdleHelperWithStaleSnapshotDoesNotQuarantineViewer(t *testing.T) {
	bound, listener := apiFixture(t)
	serveSizingPaneList(listener, "w1:p2")
	gone, _ := testSizeLock()
	healthy, healthyDone := testSizeLock()
	s := bound.sizing
	s.locks["w1:p1"], s.locks["w1:p2"] = gone, healthy
	snapshot := testSnapshot(t, "w1:t1")
	snapshot.Boot, snapshot.Revision = "boot", 100
	snapshot.Panes[1].Tab = "w1:t1"
	prior := &sizeView{sizing: s, active: true, held: map[string]bool{}, snapshot: snapshot}
	if err := prior.reconcile(); err != nil {
		t.Fatal(err)
	}
	prior.close()
	watched := make(chan struct{})
	go func() { s.watch("w1:p1", gone); close(watched) }()
	gone.cancel() // The native pane disappears before the next snapshot arrives.
	select {
	case <-watched:
	case <-time.After(time.Second):
		t.Fatal("idle pane exit did not reach failure cleanup")
	}
	fresh := &sizeView{sizing: s, active: true, held: map[string]bool{}}
	snapshot.Revision = 1
	data, _ := json.Marshal(snapshot)
	if _, err := fresh.serverFrame(wireControl("shell.snapshot.v1", string(data))); err != nil {
		t.Fatal("gone pane quarantined a fresh viewer with a stale snapshot", err)
	}
	if fresh.held["w1:p1"] || !fresh.held["w1:p2"] || healthy.refs != 1 {
		t.Fatal("stale reconnect did not keep only the surviving pane's reference")
	}
	select {
	case <-healthyDone:
		t.Fatal("removed idle pane interrupted an independent surviving pane")
	default:
	}
	fresh.close()
}
