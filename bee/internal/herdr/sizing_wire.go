package herdr

import (
	"bytes"
	"context"
	encodingbinary "encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// The relay preserves native frames, including large clipboard/graphics frames.
// Only generation-1 hello, navigation requests and snapshots are inspected.
const maxSizingFrame = 32 << 20

var errSizingWire = errors.New("invalid Herdr sizing frame")

func writeSizingFrame(w io.Writer, b []byte) error {
	var h [4]byte
	encodingbinary.LittleEndian.PutUint32(h[:], uint32(len(b)))
	if _, err := io.Copy(w, bytes.NewReader(h[:])); err != nil {
		return err
	}
	_, err := io.Copy(w, bytes.NewReader(b))
	return err
}

type wireDecoder struct {
	data []byte
	err  error
}

func (d *wireDecoder) take(n uint64) []byte {
	if n > uint64(len(d.data)) {
		d.err = errSizingWire
		return nil
	}
	b := d.data[:n]
	d.data = d.data[n:]
	return b
}
func (d *wireDecoder) number() uint64 {
	b := d.take(1)
	if len(b) == 0 {
		return 0
	}
	switch b[0] {
	case 251:
		b = d.take(2)
		if len(b) == 2 {
			return uint64(encodingbinary.LittleEndian.Uint16(b))
		}
	case 252:
		b = d.take(4)
		if len(b) == 4 {
			return uint64(encodingbinary.LittleEndian.Uint32(b))
		}
	case 253:
		b = d.take(8)
		if len(b) == 8 {
			return encodingbinary.LittleEndian.Uint64(b)
		}
	case 254, 255:
		d.err = errSizingWire
	default:
		return uint64(b[0])
	}
	return 0
}
func (d *wireDecoder) text() string { return string(d.take(d.number())) }

// Relay keeps sizing references until both stream directions have stopped.
// Closing either side or cancelling publication releases all helpers, including
// while a consumer is not reading output. No input is replayed on reconnect.
func (b Bound) Relay(ctx context.Context, remote io.ReadWriteCloser, local net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v := &sizeView{sizing: b.sizing, ctx: ctx, held: map[string]bool{}, firstReady: make(chan struct{})}
	defer v.close()
	stop := context.AfterFunc(ctx, func() { local.Close(); remote.Close() })
	defer stop()
	done := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	copyFrames := func(dst io.Writer, src io.Reader, inspect func([]byte) ([]byte, error), client bool) {
		defer wg.Done()
		for {
			if err := relaySizingFrame(dst, src, inspect, client); err != nil {
				done <- err
				return
			}
		}
	}
	upstream := &synchronizedWriter{w: local}
	v.sendUp = func(p []byte) error {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		return writeSizingFrame(local, p)
	}
	go copyFrames(upstream, remote, v.clientFrame, true)
	go copyFrames(remote, local, v.serverFrame, false)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var err error
	waiting := true
	for waiting {
		select {
		case err = <-done:
			waiting = false
		case <-tick.C:
			v.mu.Lock()
			err = v.reconcile()
			v.mu.Unlock()
			if err != nil {
				waiting = false
			}
		case <-ctx.Done():
			err = ctx.Err()
			waiting = false
		}
	}
	cancel()
	local.Close()
	remote.Close()
	wg.Wait()
	return err
}

// Opaque native surfaces and clipboard payloads stream with bounded buffers;
// only small navigation and snapshot frames are materialized for inspection.
func relaySizingFrame(dst io.Writer, src io.Reader, inspect func([]byte) ([]byte, error), client bool) error {
	var h [5]byte
	if _, err := io.ReadFull(src, h[:]); err != nil {
		return err
	}
	n := encodingbinary.LittleEndian.Uint32(h[:4])
	if n == 0 || n > maxSizingFrame {
		return errSizingWire
	}
	if h[4] == 20 || (client && h[4] == 15) || (!client && h[4] == 18) {
		if n > 2<<20 {
			return errSizingWire
		}
		p := make([]byte, n)
		p[0] = h[4]
		if _, err := io.ReadFull(src, p[1:]); err != nil {
			return err
		}
		p, err := inspect(p)
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		unlock := lockSizingWriter(dst)
		defer unlock()
		return writeSizingFrame(dst, p)
	}
	unlock := lockSizingWriter(dst)
	defer unlock()
	if _, err := io.Copy(dst, bytes.NewReader(h[:])); err != nil {
		return err
	}
	_, err := io.CopyN(dst, src, int64(n)-1)
	return err
}

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *synchronizedWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
func lockSizingWriter(w io.Writer) func() {
	if s, ok := w.(*synchronizedWriter); ok {
		s.mu.Lock()
		return s.mu.Unlock
	}
	return func() {}
}
func wireNumber(n uint64) []byte {
	if n < 251 {
		return []byte{byte(n)}
	}
	b := make([]byte, 9)
	b[0] = 253
	encodingbinary.LittleEndian.PutUint64(b[1:], n)
	return b
}
func wireText(s string) []byte { return append(wireNumber(uint64(len(s))), []byte(s)...) }
func wireControl(kind, data string) []byte {
	return append(append([]byte{20}, wireText(kind)...), wireText(data)...)
}
