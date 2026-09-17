// Package gateway implements the frozen Herdr endpoint generation 1 codecs.
// Source contract: herdrdev/herdr v0.9.0 src/protocol/{wire,endpoint}.rs.
package gateway

import (
	"encoding/binary"
	"errors"
	"io"
)

const MaxFrame = 2 << 20

var errWire = errors.New("invalid Herdr endpoint frame")

func Read(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return nil, e
	}
	n := binary.LittleEndian.Uint32(h[:])
	if n == 0 || n > MaxFrame {
		return nil, errWire
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	return b, e
}
func Write(w io.Writer, b []byte) error {
	if len(b) == 0 || len(b) > MaxFrame {
		return errWire
	}
	var h [4]byte
	binary.LittleEndian.PutUint32(h[:], uint32(len(b)))
	if _, e := w.Write(h[:]); e != nil {
		return e
	}
	_, e := w.Write(b)
	return e
}
func number(v uint64) []byte {
	switch {
	case v < 251:
		return []byte{byte(v)}
	case v <= 65535:
		b := []byte{251, 0, 0}
		binary.LittleEndian.PutUint16(b[1:], uint16(v))
		return b
	case v <= 4294967295:
		b := make([]byte, 5)
		b[0] = 252
		binary.LittleEndian.PutUint32(b[1:], uint32(v))
		return b
	default:
		b := make([]byte, 9)
		b[0] = 253
		binary.LittleEndian.PutUint64(b[1:], v)
		return b
	}
}
func str(s string) []byte { return append(number(uint64(len(s))), []byte(s)...) }
func control(kind string, data []byte) []byte {
	b := []byte{20}
	b = append(b, str(kind)...)
	return append(b, str(string(data))...)
}

type decoder struct {
	b   []byte
	p   int
	out []byte
	err error
}

func (d *decoder) raw(n int) []byte {
	if n < 0 || n > len(d.b)-d.p {
		d.err = errWire
		return nil
	}
	v := d.b[d.p : d.p+n]
	d.p += n
	d.out = append(d.out, v...)
	return v
}
func (d *decoder) num() uint64 {
	b := d.raw(1)
	if len(b) == 0 {
		return 0
	}
	switch b[0] {
	case 251:
		b = d.raw(2)
		if len(b) == 2 {
			return uint64(binary.LittleEndian.Uint16(b))
		}
	case 252:
		b = d.raw(4)
		if len(b) == 4 {
			return uint64(binary.LittleEndian.Uint32(b))
		}
	case 253:
		b = d.raw(8)
		if len(b) == 8 {
			return binary.LittleEndian.Uint64(b)
		}
	case 254, 255:
		d.err = errWire
	default:
		return uint64(b[0])
	}
	return 0
}
func (d *decoder) text() string {
	n := d.num()
	if n > MaxFrame {
		d.err = errWire
		return ""
	}
	return string(d.raw(int(n)))
}
func (d *decoder) replaceText(f func(string) string) string {
	start := len(d.out)
	s := d.text()
	d.out = d.out[:start]
	d.out = append(d.out, str(f(s))...)
	return s
}
func (d *decoder) replaceNum(v uint64) uint64 {
	start := len(d.out)
	old := d.num()
	d.out = d.out[:start]
	d.out = append(d.out, number(v)...)
	return old
}
func (d *decoder) many(fn func()) {
	n := d.num()
	if n > MaxFrame || n > uint64(len(d.b)-d.p) {
		d.err = errWire
		return
	}
	for i := uint64(0); i < n && d.err == nil; i++ {
		fn()
	}
}
func (d *decoder) option(fn func()) {
	v := d.raw(1)
	if len(v) == 0 {
		return
	}
	if v[0] == 1 {
		fn()
	} else if v[0] != 0 {
		d.err = errWire
	}
}
func (d *decoder) enum(max uint64) {
	if d.num() > max {
		d.err = errWire
	}
}
func (d *decoder) notification(tag uint64) {
	switch tag {
	case 4:
		d.enum(2) // NotifyKind: sound, toast, system toast.
		d.text()
		d.option(func() { d.text() })
	case 14:
		d.enum(3) // SemanticNotificationKind.
		d.text()
		d.option(func() { d.text() })
		d.option(func() { d.enum(1) }) // SemanticNotificationSound.
		for range 4 {                  // Agent, workspace, tab and pane identifiers.
			d.option(func() { d.text() })
		}
		d.option(func() { d.enum(3) }) // ToastHerdrPosition.
	default:
		d.err = errWire
	}
}
func (d *decoder) nums(n int) {
	for i := 0; i < n; i++ {
		d.num()
	}
}
func (d *decoder) cell()   { d.text(); d.nums(3); d.raw(1); d.option(func() { d.num() }) }
func (d *decoder) cursor() { d.nums(2); d.raw(2) }
func (d *decoder) frame() {
	d.many(d.cell)
	d.nums(2)
	d.option(d.cursor)
	d.many(func() { d.text() })
	d.text()
}
func (d *decoder) pane(id func(string) string) {
	d.replaceText(id)
	d.nums(9)
	d.option(func() { d.nums(4) })
	d.option(func() { d.nums(3) })
	d.raw(4)
	d.nums(2)
}
func (d *decoder) asset(id func(string) string) {
	switch d.num() {
	case 0:
		if d.num() > 1 {
			d.err = errWire
		}
		d.replaceText(id)
		d.num()
	case 1:
		d.replaceText(id)
		d.text()
	default:
		d.err = errWire
	}
	d.nums(5)
}

// surface rewrites identifiers structurally; terminal text/image bytes remain opaque.
func surface(b []byte, boot string, revision uint64, id func(string) string) ([]byte, error) {
	d := decoder{b: b}
	tag := d.num()
	d.replaceText(func(string) string { return boot })
	d.replaceNum(revision)
	if tag == 13 {
		d.num()
		d.frame()
		d.many(func() { d.pane(id) })
		d.many(func() { d.nums(10); d.many(func() { d.raw(1) }) })
		d.option(func() {
			d.replaceText(id)
			d.text()
			size := func() {
				if d.num() == 0 {
					d.num()
				} else {
					d.raw(1)
				}
			}
			d.option(size)
			d.option(size)
			d.frame()
			d.raw(2)
			d.nums(2)
		})
		d.many(func() { d.asset(id); d.text() })
		d.many(func() { d.asset(id); d.nums(13) })
		d.many(func() { d.asset(id) })
	} else if tag == 19 {
		d.nums(2)
		d.many(func() { d.nums(2); d.many(d.cell) })
		d.many(func() { d.pane(id) })
		d.option(d.cursor)
	} else {
		return nil, errWire
	}
	if d.err != nil || d.p != len(b) {
		return nil, errWire
	}
	return d.out, nil
}

// completeSurface applies incremental rows before switching providers, so a switch
// always starts with a full frame and never depends on another provider's base.
type completeSurface struct {
	assets        map[string][]byte
	header        []byte
	cells         [][]byte
	width, height uint64
	cursor, tail  []byte
	revision      uint64
	panes         []byte
}

func cut(d *decoder, fn func()) []byte { start := d.p; fn(); return d.b[start:d.p] }
func (s *completeSurface) update(b []byte) error {
	d := decoder{b: b}
	tag := d.num()
	boot := d.text()
	projection := d.num()
	if tag == 13 {
		s.revision = d.num()
		s.header = append([]byte(nil), b[:d.p]...)
		s.cells = nil
		check := decoder{b: b[d.p:]}
		if check.num() > 65536 {
			return errWire
		}
		d.many(func() { s.cells = append(s.cells, append([]byte(nil), cut(&d, d.cell)...)) })
		s.width = d.num()
		s.height = d.num()
		s.cursor = append([]byte(nil), cut(&d, func() { d.option(d.cursor) })...)
		// Hyperlinks and legacy graphics precede the pane metadata.
		s.tail = append([]byte(nil), b[d.p:]...)
		if e := s.retainGraphics(); e != nil {
			return e
		}
	} else if tag == 19 {
		base := d.num()
		rev := d.num()
		if s.header == nil || base != s.revision {
			return errWire
		}
		d.many(func() {
			x, y := d.num(), d.num()
			n := d.num()
			if y >= s.height || x+n > s.width {
				d.err = errWire
				return
			}
			for i := uint64(0); i < n && d.err == nil; i++ {
				idx := y*s.width + x + i
				if idx >= uint64(len(s.cells)) {
					d.err = errWire
					return
				}
				s.cells[idx] = append([]byte(nil), cut(&d, d.cell)...)
			}
		})
		// Patch pane metadata is a replacement by pane ID.
		updates := map[string][]byte{}
		d.many(func() {
			start := d.p
			id := d.text()
			d.p = start
			d.pane(func(s string) string { return s })
			updates[id] = append([]byte(nil), b[start:d.p]...)
		})
		s.cursor = append([]byte(nil), cut(&d, func() { d.option(d.cursor) })...)
		t := decoder{b: s.tail}
		t.many(func() { t.text() })
		t.text()
		start := t.p
		n := t.num()
		out := append([]byte(nil), s.tail[:start]...)
		out = append(out, number(n)...)
		for i := uint64(0); i < n && t.err == nil; i++ {
			start = t.p
			id := t.text()
			t.p = start
			t.pane(func(s string) string { return s })
			v := s.tail[start:t.p]
			if p, ok := updates[id]; ok {
				v = p
			}
			out = append(out, v...)
		}
		if t.err != nil {
			return t.err
		}
		s.tail = append(out, s.tail[t.p:]...)
		s.revision = rev
		s.header = append([]byte{13}, str(boot)...)
		s.header = append(s.header, number(projection)...)
		s.header = append(s.header, number(rev)...)
	} else {
		return errWire
	}
	if d.err != nil {
		return d.err
	}
	retained := len(s.header) + len(s.cursor) + len(s.tail)
	for _, v := range s.cells {
		retained += len(v)
	}
	if retained > MaxFrame {
		return errWire
	}
	if s.width*s.height != uint64(len(s.cells)) {
		return errWire
	}
	return nil
}
func (s *completeSurface) bytes() []byte {
	if s.header == nil {
		return nil
	}
	b := append([]byte(nil), s.header...)
	b = append(b, number(uint64(len(s.cells)))...)
	for _, c := range s.cells {
		b = append(b, c...)
	}
	b = append(b, number(s.width)...)
	b = append(b, number(s.height)...)
	b = append(b, s.cursor...)
	return append(b, s.tail...)
}

func (d *decoder) split() { d.nums(10); d.many(func() { d.raw(1) }) }
func (d *decoder) popup(id func(string) string) {
	d.replaceText(id)
	d.text()
	size := func() {
		switch d.num() {
		case 0:
			d.num()
		case 1:
			d.raw(1)
		default:
			d.err = errWire
		}
	}
	d.option(size)
	d.option(size)
	d.frame()
	d.raw(2)
	d.nums(2)
}

// retainGraphics expands the incremental assets in a full scene into a bounded
// complete set, dropping assets no longer referenced by placements or retention.
func (s *completeSurface) retainGraphics() error {
	d := decoder{b: s.tail}
	id := func(s string) string { return s }
	d.many(func() { d.text() })
	d.text()
	d.many(func() { d.pane(id) })
	d.many(d.split)
	d.option(func() { d.popup(id) })
	start := d.p
	incoming := map[string][]byte{}
	d.many(func() {
		key := string(cut(&d, func() { d.asset(id) }))
		incoming[key] = append([]byte(nil), cut(&d, func() { d.text() })...)
	})
	rest := d.p
	keys := []string{}
	seen := map[string]bool{}
	add := func() {
		key := string(cut(&d, func() { d.asset(id) }))
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	d.many(func() { add(); d.nums(13) })
	d.many(add)
	if d.err != nil || d.p != len(s.tail) {
		return errWire
	}
	out := append([]byte(nil), s.tail[:start]...)
	out = append(out, number(uint64(len(keys)))...)
	keep := map[string][]byte{}
	for _, key := range keys {
		v, ok := incoming[key]
		if !ok {
			v, ok = s.assets[key]
		}
		if !ok {
			return errWire
		}
		keep[key] = v
		out = append(out, []byte(key)...)
		out = append(out, v...)
		if len(out) > MaxFrame {
			return errWire
		}
	}
	s.assets = keep
	s.tail = append(out, s.tail[rest:]...)
	return nil
}
