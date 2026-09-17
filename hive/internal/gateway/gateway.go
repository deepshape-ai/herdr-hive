package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// MaxSources bounds per-viewer upstream connections and retained complete surfaces.
const MaxSources = 8

// Source.Open must honor cancellation, including while establishing a stream.
type Source struct {
	ID, Name, Label string
	Open            func(context.Context) (io.ReadWriteCloser, error)
}
type Catalog func() []Source

type backend struct {
	effects       map[uint64][]byte
	welcomed      bool
	surfaceActive bool
	source        Source
	stream        io.ReadWriteCloser
	boot, prefix  string
	snapshot      map[string]any
	methods       map[string]bool
	frame         completeSurface
	chunks        map[string][]byte
	cancel        context.CancelFunc
}
type event struct {
	stream io.ReadWriteCloser
	source *backend
	data   []byte
	err    error
}
type session struct {
	ctx              context.Context
	cancel           context.CancelFunc
	out              io.ReadWriteCloser
	catalog          Catalog
	events           chan event
	sources          map[string]*backend
	active           *backend
	boot             string
	revision         uint64
	hello            []byte
	welcome          bool
	resize           []byte
	surfaceActive    bool
	surfaceRevision  uint64
	interestSequence uint64
	pending          map[string]*backend
	fence            string
	fencePending     bool
	fenceSource      *backend
	fenceUntil       time.Time
	deadlines        map[string]time.Time
	cols, rows       uint64
}

// Serve presents the visible Bee sessions as one stable Herdr endpoint. All
// mutable projection/routing state belongs to this event loop, not goroutines.
func Serve(ctx context.Context, out io.ReadWriteCloser, catalog Catalog) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	g := &session{ctx: ctx, cancel: cancel, out: out, catalog: catalog, events: make(chan event, 2), sources: map[string]*backend{}, pending: map[string]*backend{}, deadlines: map[string]time.Time{}, boot: "hive-" + hex.EncodeToString(nonce)}
	defer func() {
		cancel()
		for _, b := range g.sources {
			b.cancel()
			if b.stream != nil {
				b.stream.Close()
			}
		}
	}()
	// Closing the downstream releases the reader on every exit path.
	defer out.Close()
	go g.read(nil, out)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case ev := <-g.events:
		if ev.err != nil {
			return ev.err
		}
		d := decoder{b: ev.data}
		if d.num() != 20 || d.text() != "endpoint.hello.v1" {
			return errWire
		}
		var h map[string]any
		if json.Unmarshal([]byte(d.text()), &h) != nil || d.err != nil || d.p != len(d.b) || h["generation"] != float64(1) {
			return errWire
		}
		for field, codec := range map[string]string{"snapshot_codecs": "shell.snapshot.v1", "surface_codecs": "shell.surface.v1", "input_codecs": "shell.input.semantic.v1", "blob_codecs": "shell.blob.v1"} {
			ok := false
			for _, v := range array(h[field]) {
				ok = ok || v == codec
			}
			if !ok {
				return errWire
			}
		}
		g.surfaceActive = h["surface_active"] != false
		size := object(h["surface_size"])
		g.cols = uint64(numberVal(size["cols"]))
		g.rows = uint64(numberVal(size["rows"]))
		if g.cols*g.rows > 65536 {
			return errWire
		}
		h["direct_graphics"] = false
		// Background subscriptions must not acquire publisher layout authority.
		h["surface_active"] = false
		g.resize = append(number(12), number(uint64(numberVal(h["cell_width_px"])))...)
		g.resize = append(g.resize, number(uint64(numberVal(h["cell_height_px"])))...)
		g.resize = append(g.resize, number(g.cols)...)
		g.resize = append(g.resize, number(g.rows)...)
		pixelMouse := byte(0)
		if h["pixel_mouse"] == true {
			pixelMouse = 1
		}
		g.resize = append(g.resize, pixelMouse)
		p, _ := json.Marshal(h)
		g.hello = control("endpoint.hello.v1", p)
	case <-timer.C:
		return errors.New("endpoint handshake timed out")
	case <-ctx.Done():
		return ctx.Err()
	}
	// The gateway advertises only operations it can route; upstream support is
	// checked again for each call; presentation fences follow the active source.
	welcome := map[string]any{"generation": 1, "server_version": "0.9.0", "snapshot_codec": "shell.snapshot.v1", "surface_codec": "shell.surface.v1", "input_codec": "shell.input.semantic.v1", "blob_codec": "shell.blob.v1", "methods": supportedMethods, "capabilities": []string{"surface_interest", "presentation_effects_fence", "health_check"}}
	if e := g.jsonControl("endpoint.welcome.v1", welcome); e != nil {
		return e
	}
	if e := g.publish(); e != nil {
		return e
	}
	g.refresh()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			g.refresh()
		case ev := <-g.events:
			if ev.source == nil {
				if ev.err != nil {
					return ev.err
				}
				if e := g.client(ev.data); e != nil {
					return e
				}
				continue
			}
			b := ev.source
			if g.sources[b.source.ID] != b {
				if ev.stream != nil {
					ev.stream.Close()
				}
				continue
			}
			if ev.err != nil {
				g.remove(b)
				if e := g.publish(); e != nil {
					return e
				}
				continue
			}
			if ev.stream != nil {
				b.stream = ev.stream
				continue
			}
			if ev.data == nil {
				continue
			}
			if e := g.server(b, ev.data); e != nil {
				g.remove(b)
				if e = g.publish(); e != nil {
					return e
				}
			}
		}
	}
}
func (g *session) read(b *backend, r io.Reader) {
	for {
		data, e := Read(r)
		select {
		case g.events <- event{source: b, data: data, err: e}:
		case <-g.ctx.Done():
			return
		}
		if e != nil {
			return
		}
	}
}
func (g *session) remove(b *backend) {
	if g.fenceSource == b {
		g.fenceSource = nil
	}
	for id, target := range g.pending {
		if target == b {
			delete(g.pending, id)
			delete(g.deadlines, id)
			if !internalInterest(id) && g.failure(id, "sharing disconnected") != nil {
				g.cancel()
			}
		}
	}
	b.cancel()
	if b.stream != nil {
		b.stream.Close()
	}
	delete(g.sources, b.source.ID)
	if g.active == b {
		g.active = nil
	}
}
func (g *session) refresh() {
	if g.fenceSource != nil && time.Now().After(g.fenceUntil) {
		g.remove(g.fenceSource)
		if g.publish() != nil {
			g.cancel()
		}
	}
	for id, t := range g.deadlines {
		if time.Now().After(t) {
			b := g.pending[id]
			delete(g.pending, id)
			delete(g.deadlines, id)
			delete(b.chunks, id)
			if internalInterest(id) {
				g.remove(b)
				if g.publish() != nil {
					g.cancel()
				}
				continue
			}
			if g.failure(id, "sharing operation timed out") != nil {
				g.cancel()
			}
		}
	}
	list := g.catalog()
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	if len(list) > MaxSources {
		list = list[:MaxSources]
	}
	visible := map[string]bool{}
	changed := false
	for _, src := range list {
		visible[src.ID] = true
		if old := g.sources[src.ID]; old != nil {
			if old.source.Name != src.Name || old.source.Label != src.Label {
				old.source.Name = src.Name
				old.source.Label = src.Label
				changed = true
			}
			continue
		}
		ctx, cancel := context.WithCancel(g.ctx)
		b := &backend{source: src, cancel: cancel, chunks: map[string][]byte{}, methods: map[string]bool{}, effects: map[uint64][]byte{}}
		g.sources[src.ID] = b
		go func() {
			stream, e := src.Open(ctx)
			if e == nil {
				e = Write(stream, g.hello)
				if e == nil {
					select {
					case g.events <- event{source: b, stream: stream}:
					case <-ctx.Done():
						stream.Close()
						return
					}
					g.read(b, stream)
					return
				}
				stream.Close()
			}
			select {
			case g.events <- event{source: b, err: e}:
			case <-g.ctx.Done():
			}
		}()
	}
	for id, b := range g.sources {
		if !visible[id] {
			g.remove(b)
			changed = true
		}
	}
	if changed {
		if g.publish() != nil {
			g.cancel()
		}
	}
}
func array(v any) []any           { a, _ := v.([]any); return a }
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func stringVal(v any) string      { s, _ := v.(string); return s }
func (g *session) jsonControl(kind string, v any) error {
	p, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return Write(g.out, control(kind, p))
}
func emptySnapshot() map[string]any {
	return map[string]any{"boot_id": "", "revision": 0, "config_diagnostic": nil, "product_announcement": nil, "update_available": nil, "update_install_command": "", "server_keybindings_toml": nil, "latest_release_notes_available": false, "integration_updates_available": false, "worktree_directory": "", "release_notes": nil, "focused_workspace_id": nil, "focused_tab_id": nil, "focused_pane_id": nil, "tab_bar_right": []any{}, "tab_bar_right_separator": " | ", "agent_view_label": nil, "agent_order": []any{}, "workspaces": []any{}, "tabs": []any{}, "panes": []any{}, "agents": []any{}, "commands": []any{}}
}
func isID(k string) bool {
	switch k {
	case "workspace_id", "tab_id", "pane_id", "terminal_id", "command_id", "source_workspace_id", "target_pane_id", "source_pane_id", "before_workspace_id", "active_tab_id", "focused_workspace_id", "focused_tab_id", "focused_pane_id":
		return true
	}
	return false
}
func transform(v any, f func(string) string) any {
	switch x := v.(type) {
	case map[string]any:
		o := map[string]any{}
		for k, v := range x {
			if isID(k) && v != nil {
				o[k] = f(stringVal(v))
			} else if k == "agent_order" {
				a := []any{}
				for _, id := range array(v) {
					a = append(a, f(stringVal(id)))
				}
				o[k] = a
			} else {
				o[k] = transform(v, f)
			}
		}
		return o
	case []any:
		a := make([]any, len(x))
		for i, v := range x {
			a[i] = transform(v, f)
		}
		return a
	default:
		return v
	}
}
func (g *session) snapshotIDs() []string {
	ids := []string{}
	for id, b := range g.sources {
		if b.snapshot != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
func (g *session) publish() error {
	ids := g.snapshotIDs()
	if g.active == nil && len(ids) > 0 {
		g.active = g.sources[ids[0]]
		if e := g.replayEffects(); e != nil {
			return e
		}
	}
	if e := g.syncInterest(); e != nil {
		return e
	}
	// Writes can remove a failed source and publish its replacement.
	ids = g.snapshotIDs()
	out := emptySnapshot()
	if g.active != nil {
		out = object(transform(g.active.snapshot, func(id string) string { return g.active.prefix + id }))
	}
	for _, k := range []string{"workspaces", "tabs", "panes", "agents", "agent_order"} {
		out[k] = []any{}
	}
	for _, id := range ids {
		b := g.sources[id]
		s := object(transform(b.snapshot, func(id string) string { return b.prefix + id }))
		for _, k := range []string{"workspaces", "tabs", "panes", "agents"} {
			for _, v := range array(s[k]) {
				m := object(v)
				if m == nil {
					continue
				}
				if b != g.active {
					m["focused"] = false
				}
				if k == "workspaces" {
					source := b.source.Name
					if b.source.Label != "default" {
						source += "/" + b.source.Label
					}
					m["label"] = fmt.Sprintf("[%s] %s", source, stringVal(m["label"]))
					m["custom_label"] = true
					m["number"] = len(array(out[k])) + 1
				}
				out[k] = append(array(out[k]), m)
			}
		}
		out["agent_order"] = append(array(out["agent_order"]), array(s["agent_order"])...)
	}
	g.revision++
	out["boot_id"] = g.boot
	out["revision"] = g.revision
	// Endpoint-owned updates/configuration do not describe the virtual Hive host.
	if len(g.catalog()) > MaxSources {
		out["config_diagnostic"] = fmt.Sprintf("Hive gateway shows the first %d shared sessions; use a sharing ID for the remaining sessions.", MaxSources)
	}
	out["product_announcement"] = nil
	out["update_available"] = nil
	out["latest_release_notes_available"] = false
	out["integration_updates_available"] = false
	if e := g.jsonControl("shell.snapshot.v1", out); e != nil {
		return e
	}
	if e := g.sendSurface(); e != nil {
		return e
	}
	return g.resumeFence()
}
func (g *session) sendSurface() error {
	if !g.surfaceActive {
		return nil
	}
	b := g.active
	if b == nil {
		return g.blankSurface()
	}
	raw := b.frame.bytes()
	if raw == nil {
		return nil
	}
	d := decoder{b: raw}
	d.num()
	boot := d.text()
	rev := d.num()
	if boot != b.boot || rev != uint64(numberVal(b.snapshot["revision"])) {
		return nil
	}
	p, e := surface(raw, g.boot, g.revision, func(id string) string { return b.prefix + id })
	if e != nil {
		return e
	}
	d = decoder{b: p}
	d.num()
	d.text()
	d.num()
	g.surfaceRevision++
	d.replaceNum(g.surfaceRevision)
	p = append(d.out, p[d.p:]...)
	return Write(g.out, p)
}
func numberVal(v any) float64 { n, _ := v.(float64); return n }
func (g *session) server(b *backend, p []byte) error {
	d := decoder{b: p}
	tag := d.num()
	switch tag {
	case 20:
		kind, data := d.text(), d.text()
		if d.err != nil || d.p != len(p) {
			return errWire
		}
		if kind == "endpoint.presentation.ready.v1" {
			if b == g.fenceSource && data == g.fence {
				g.fence = ""
				g.fencePending = false
				g.fenceSource = nil
				return Write(g.out, control(kind, []byte(data)))
			}
			return nil
		}
		var v map[string]any
		if len(data) > 256<<10 {
			return errWire
		}
		if json.Unmarshal([]byte(data), &v) != nil {
			return errWire
		}
		switch kind {
		case "endpoint.welcome.v1":
			if b.welcomed {
				return errWire
			}
			if v["error"] != nil || v["generation"] != float64(1) {
				return errWire
			}
			b.welcomed = true
			for _, m := range array(v["methods"]) {
				if allowedMethod(stringVal(m)) {
					b.methods[stringVal(m)] = true
				}
			}
		case "shell.snapshot.v1":
			if !b.welcomed {
				return errWire
			}
			boot := stringVal(v["boot_id"])
			if boot == "" {
				return errWire
			}
			if len(boot) > 128 {
				return errWire
			}
			if b.boot != "" && b.boot != boot {
				return errWire
			}
			b.boot = boot
			hash := sha256.Sum256([]byte(b.source.ID + "\x00" + boot))
			b.prefix = "g" + hex.EncodeToString(hash[:8]) + "/"
			b.snapshot = v
			return g.publish()
		}
		return nil
	case 13, 19:
		// Validate the complete fixed codec before retaining anything.
		if _, e := surface(p, g.boot, g.revision, func(id string) string { return id }); e != nil {
			return e
		}
		if e := b.frame.update(p); e != nil {
			return e
		}
		if b == g.active {
			return g.sendSurface()
		}
		if g.active == nil {
			return g.blankSurface()
		}
		return nil
	case 18:
		boot, id := d.text(), d.text()
		if g.pending[id] != b {
			return errWire
		}
		final := d.raw(1)
		data := d.text()
		if d.err != nil || d.p != len(p) || boot != b.boot {
			return errWire
		}
		total := len(data)
		for _, v := range b.chunks {
			total += len(v)
		}
		if total > MaxFrame || len(b.chunks) >= 64 {
			return errWire
		}
		b.chunks[id] = append(b.chunks[id], data...)
		if len(final) == 0 || final[0] != 1 {
			return nil
		}
		data = string(b.chunks[id])
		delete(b.chunks, id)
		delete(g.pending, id)
		delete(g.deadlines, id)
		var v any
		if json.Unmarshal([]byte(data), &v) != nil {
			return errWire
		}
		if internalInterest(id) {
			if object(v) == nil || object(v)["error"] != nil {
				return errors.New("publisher rejected surface interest")
			}
			return nil
		}
		v = transform(v, func(id string) string { return b.prefix + id })
		if obj := object(v); obj != nil {
			obj["id"] = id
		} else {
			return errWire
		}
		return g.response(id, v)
	case 3:
		return io.EOF
	case 4, 14:
		// Aggregate viewers observe remote agent state through snapshots. Do not
		// mix remote completion/attention sounds and toasts into the viewer's
		// local notifications. Validate the frozen codec before discarding it.
		d.notification(tag)
		if d.err != nil || d.p != len(p) {
			return errWire
		}
		return nil
	case 5, 6, 8, 9, 15, 17:
		if tag == 6 || tag == 8 || tag == 17 {
			b.effects[tag] = append([]byte(nil), p...)
		}
		if b == g.active && g.surfaceActive {
			return Write(g.out, p)
		}
	}
	return nil
}
func (g *session) response(id string, v any) error {
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	p := append([]byte{18}, str(g.boot)...)
	p = append(p, str(id)...)
	p = append(p, 1)
	p = append(p, str(string(data))...)
	return Write(g.out, p)
}
func (g *session) failure(id, message string) error {
	return g.response(id, map[string]any{"id": id, "error": map[string]any{"code": "invalid_request", "message": message}})
}
func (g *session) resolve(id string) (*backend, string) {
	for _, b := range g.sources {
		if b.prefix != "" && strings.HasPrefix(id, b.prefix) {
			return b, strings.TrimPrefix(id, b.prefix)
		}
	}
	return nil, ""
}
func (g *session) send(b *backend, p []byte) error {
	if b == nil || b.stream == nil {
		return errors.New("shared session is offline")
	}
	if e := Write(b.stream, p); e != nil {
		g.remove(b)
		return g.publish()
	}
	return nil
}
func (g *session) client(p []byte) error {
	d := decoder{b: p}
	tag := d.num()
	switch tag {
	case 20:
		kind, data := d.text(), d.text()
		if d.err != nil {
			return errWire
		}
		if kind == "endpoint.presentation.sync.v1" {
			if len(data) > 4096 {
				return errWire
			}
			if g.active == nil {
				return Write(g.out, control("endpoint.presentation.ready.v1", []byte(data)))
			}
			if g.fencePending {
				return errWire
			}
			g.fence = data
			g.fencePending = true
			g.fenceSource = nil
			return g.resumeFence()
		}
		if kind == "endpoint.health.ping.v1" {
			return Write(g.out, control("endpoint.health.pong.v1", []byte(data)))
		}
		return nil
	case 13, 14:
		id := d.text()
		b, local := g.resolve(id)
		if b == nil || b != g.active || !g.surfaceActive {
			return nil
		}
		q := append(number(tag), str(local)...)
		q = append(q, p[d.p:]...)
		return g.send(b, q)
	case 2:
		target := d.num()
		if target != 1 && target != 2 {
			return nil
		}
		id := d.text()
		b, local := g.resolve(id)
		if b == nil || b != g.active || !g.surfaceActive {
			return nil
		}
		q := append(number(tag), number(target)...)
		q = append(q, str(local)...)
		q = append(q, p[d.p:]...)
		return g.send(b, q)
	case 15:
		boot, request := d.text(), d.text()
		if d.err != nil || d.p != len(p) {
			return errWire
		}
		var v map[string]any
		if json.Unmarshal([]byte(request), &v) != nil {
			return errWire
		}
		id, method := stringVal(v["id"]), stringVal(v["method"])
		if id == "" || len(id) > 128 || internalInterest(id) {
			return errWire
		}
		if boot != g.boot {
			return g.failure(id, "stale Hive endpoint")
		}
		if method == "client_shell.surface.set" {
			active, ok := object(v["params"])["active"].(bool)
			if !ok {
				return g.failure(id, "active must be boolean")
			}
			g.surfaceActive = active
			if active {
				if e := g.replayEffects(); e != nil {
					return e
				}
			}
			if e := g.publish(); e != nil {
				return e
			}
			return g.response(id, map[string]any{"id": id, "result": map[string]any{"type": "client_shell_surface_set", "active": active, "projection_revision": g.revision}})
		}
		if !allowedMethod(method) {
			return g.failure(id, "operation is not supported by the Hive gateway")
		}
		var target *backend
		invalid := false
		v = object(transform(v, func(id string) string {
			b, local := g.resolve(id)
			if b == nil || (target != nil && target != b) {
				invalid = true
				return ""
			}
			target = b
			return local
		}))
		if invalid {
			return g.failure(id, "unknown or mixed sharing targets")
		}
		if target == nil {
			target = g.active
		}
		if target == nil || !target.methods[method] {
			return g.failure(id, "operation unavailable on this sharing")
		}
		if strings.HasSuffix(method, ".focus") && target != g.active {
			g.active = target
			if e := g.replayEffects(); e != nil {
				return e
			}
			if e := g.publish(); e != nil {
				return e
			}
		}
		if len(g.pending) >= 64 {
			return g.failure(id, "too many outstanding operations")
		}
		if g.pending[id] != nil {
			return errWire
		}
		g.pending[id] = target
		g.deadlines[id] = time.Now().Add(30 * time.Second)
		q, _ := json.Marshal(v)
		out := append([]byte{15}, str(target.boot)...)
		out = append(out, str(string(q))...)
		return g.send(target, out)
	case 12:
		d.nums(2)
		cols, rows := d.num(), d.num()
		pixelMouse := d.raw(1)
		if len(pixelMouse) != 1 || pixelMouse[0] > 1 {
			return errWire
		}
		if cols == 0 || rows == 0 || cols > 65536 || rows > 65536 || cols*rows > 65536 || d.err != nil || d.p != len(p) {
			return errWire
		}
		g.cols, g.rows = cols, rows
		g.resize = append([]byte(nil), p...)
		if g.active != nil && g.surfaceActive {
			return g.send(g.active, p)
		}
		return g.blankSurface()
	case 17, 19:
		for _, b := range g.sources {
			if b.stream != nil {
				if e := g.send(b, p); e != nil {
					g.remove(b)
				}
			}
		}
		if g.active == nil {
			return g.blankSurface()
		}
		return nil
	case 18:
		if g.active != nil {
			return g.send(g.active, p)
		}
		return nil
	case 4:
		return io.EOF
	default:
		return errWire
	}
}

var supportedMethods = []string{"client_shell.surface.set", "command.invoke", "layout.set_split_ratio", "workspace.focus", "workspace.create", "workspace.rename", "workspace.close", "tab.focus", "tab.create", "tab.rename", "tab.close", "tab.move", "pane.focus", "pane.focus_direction", "pane.split", "pane.close", "pane.rename", "pane.resize", "pane.scroll", "pane.copy_motion", "pane.copy_search", "pane.edit_scrollback", "pane.input.set", "pane.link.activate", "pane.selection.read", "pane.swap", "pane.zoom"}

func allowedMethod(m string) bool {
	for _, v := range supportedMethods {
		if v == m {
			return true
		}
	}
	return false
}

func (g *session) blankSurface() error {
	g.surfaceRevision++
	b := append([]byte{13}, str(g.boot)...)
	b = append(b, number(g.revision)...)
	b = append(b, number(g.surfaceRevision)...)
	b = append(b, number(g.cols*g.rows)...)
	for i := uint64(0); i < g.cols*g.rows; i++ {
		b = append(b, 1, ' ', 0, 0, 0, 0, 0)
	}
	b = append(b, number(g.cols)...)
	b = append(b, number(g.rows)...)
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	return Write(g.out, b)
}

func (g *session) replayEffects() error {
	if !g.surfaceActive {
		return nil
	}
	defaults := map[uint64][]byte{6: {6, 0}, 8: {8, 0, 0}, 17: {17, 0}}
	for _, tag := range []uint64{6, 8, 17} {
		p := defaults[tag]
		if g.active != nil && g.active.effects[tag] != nil {
			p = g.active.effects[tag]
		}
		if e := Write(g.out, p); e != nil {
			return e
		}
	}
	return nil
}

func (g *session) resumeFence() error {
	if !g.fencePending || g.fenceSource != nil {
		return nil
	}
	if g.active == nil {
		token := g.fence
		g.fence = ""
		g.fencePending = false
		return Write(g.out, control("endpoint.presentation.ready.v1", []byte(token)))
	}
	g.fenceSource = g.active
	g.fenceUntil = time.Now().Add(10 * time.Second)
	return g.send(g.active, control("endpoint.presentation.sync.v1", []byte(g.fence)))
}
