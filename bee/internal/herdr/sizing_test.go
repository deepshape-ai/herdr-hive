package herdr

import (
	"bytes"
	"context"
	encodingbinary "encoding/binary"
	"encoding/json"
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
func TestSizingReferencesSurviveFirstViewerDeparture(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once
	lock := &sizeLock{done: done, cancel: func() { once.Do(func() { close(done) }) }}
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": lock}}
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
	select {
	case <-done:
	default:
		t.Fatal("last viewer leaked controller")
	}
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
	done := make(chan struct{})
	close(done)
	l := &sizeLock{refs: 1, done: done, cancel: func() {}}
	v := &sizeView{ctx: context.Background(), sizing: &Sizing{locks: map[string]*sizeLock{"w1:p1": l}}, active: true, held: map[string]bool{"w1:p1": true}, snapshot: testSnapshot(t, "w1:t1")}
	if v.reconcile() == nil {
		t.Fatal("dead sizing controller accepted")
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
			go func() { done <- b.Relay(ctx, remote, local) }()
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
	select {
	case <-ends["w1:p1"]:
	default:
		t.Fatal("old tab not released after confirmation")
	}
	if len(v.pending) != 0 {
		t.Fatal("confirmed navigation retained")
	}
	v.close()
}
func TestInitialHandshakePinsBeforeActivation(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once
	s := &Sizing{locks: map[string]*sizeLock{"w1:p1": {done: done, cancel: func() { once.Do(func() { close(done) }) }}}}
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
