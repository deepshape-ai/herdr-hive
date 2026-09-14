package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var sizeSlots = make(chan struct{}, 32) // Process-wide helper budget, across all shared sessions.

// Sizing is shared by all remote viewers of one published session. Native local
// clients keep their input path; Herdr's direct attach lock owns only PTY sizing.
// No controller uses --takeover, so an existing direct attach is never evicted.
type Sizing struct {
	mu    sync.Mutex
	bound Bound
	locks map[string]*sizeLock
}
type sizeLock struct {
	refs   int
	cancel context.CancelFunc
	done   <-chan struct{}
}
type shellSnapshot struct {
	Revision   uint64 `json:"revision"`
	Boot       string `json:"boot_id"`
	Tab        string `json:"focused_tab_id"`
	Workspaces []struct {
		ID  string `json:"workspace_id"`
		Tab string `json:"active_tab_id"`
	} `json:"workspaces"`
	Panes []struct {
		ID  string `json:"pane_id"`
		Tab string `json:"tab_id"`
	} `json:"panes"`
}
type navigation struct {
	tab   string
	seq   uint64
	ack   bool
	floor uint64
	since time.Time
}

type sizeView struct {
	mu              sync.Mutex
	sizing          *Sizing
	ctx             context.Context
	active          bool
	snapshot        shellSnapshot
	held            map[string]bool
	pending         map[string]*navigation
	sequence        uint64
	initial         bool
	initialized     bool
	activity        uint64
	hide            map[string]uint64
	sendUp          func([]byte) error
	requestedActive bool
	firstReady      chan struct{}
}

func (b Bound) api(ctx context.Context, method string, params any, result any) error {
	if err := b.Check(); err != nil {
		return err
	}
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", b.Socket)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if err = json.NewEncoder(c).Encode(map[string]any{"id": "bee-sizing", "method": method, "params": params}); err != nil {
		return err
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(c, 2<<20)).Decode(&response); err != nil {
		return err
	}
	if len(response.Error) > 0 {
		return fmt.Errorf("Herdr %s rejected sizing query", method)
	}
	return json.Unmarshal(response.Result, result)
}

// Read the actual PTY dimensions, not pane.layout's outer rect (which includes
// borders and scrollbars). Herdr 0.9.0 reports shell_pid but leaves tty null.
func (b Bound) terminalGeometry(ctx context.Context, pane string) (string, *unix.Winsize, error) {
	var p struct {
		Pane struct {
			Terminal string `json:"terminal_id"`
		} `json:"pane"`
	}
	if err := b.api(ctx, "pane.get", map[string]string{"pane_id": pane}, &p); err != nil {
		return "", nil, err
	}
	var info struct {
		Process struct {
			PID int `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := b.api(ctx, "pane.process_info", map[string]string{"pane_id": pane}, &info); err != nil {
		return "", nil, err
	}
	if info.Process.PID <= 0 || p.Pane.Terminal == "" {
		return "", nil, errors.New("pane has no live PTY")
	}
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(info.Process.PID), "-o", "tty=")
	out, err := cmd.Output()
	if err != nil {
		return "", nil, err
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || tty == "?" || tty == "??" || filepath.IsAbs(tty) || strings.Contains(tty, "..") {
		return "", nil, errors.New("pane PTY unavailable")
	}
	f, err := os.OpenFile("/dev/"+tty, os.O_RDONLY|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	size, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return "", nil, err
	}
	if size.Col == 0 || size.Row == 0 || uint64(size.Col)*uint64(size.Row) > 65536 {
		return "", nil, errors.New("pane PTY dimensions exceed sizing budget")
	}
	return p.Pane.Terminal, size, nil
}

func (b Bound) holdSize(ctx context.Context, pane string) (*sizeLock, error) {
	select {
	case sizeSlots <- struct{}{}:
	default:
		return nil, errors.New("publisher sizing limit reached (32 panes)")
	}
	started := false
	defer func() {
		if !started {
			<-sizeSlots
		}
	}()
	setup, cancelSetup := context.WithTimeout(ctx, 3*time.Second)
	defer cancelSetup()
	terminal, size, err := b.terminalGeometry(setup, pane)
	if err != nil {
		return nil, err
	}
	if err := b.Check(); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(child, binary(), "terminal", "session", "control", terminal, "--cols", strconv.Itoa(int(size.Col)), "--rows", strconv.Itoa(int(size.Row)))
	cmd.Env = append(cleanEnv(), "HERDR_SOCKET_PATH="+b.Socket)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		cancel()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		cancel()
		return nil, err
	}
	ready := make(chan error, 1)
	done := make(chan struct{})
	started = true
	go func() {
		defer func() { <-sizeSlots }()
		defer close(done)
		defer stdin.Close()
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 4096), 8<<20)
		notified := false
		for scan.Scan() {
			var frame struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(scan.Bytes(), &frame) != nil {
				break
			}
			if frame.Type == "terminal.closed" {
				break
			}
			if frame.Type == "terminal.frame" && !notified {
				// Restore coherent cell pixels as well as rows/columns. CLI's initial
				// control handshake uses zero cell pixels; dimensions remain unchanged.
				if size.Xpixel > 0 && size.Ypixel > 0 {
					err := json.NewEncoder(stdin).Encode(map[string]any{"type": "terminal.resize", "cols": size.Col, "rows": size.Row, "cell_width_px": size.Xpixel / size.Col, "cell_height_px": size.Ypixel / size.Row})
					if err != nil {
						break
					}
				}
				ready <- nil
				notified = true
			}
		}
		if !notified {
			ready <- errors.New("terminal size controller unavailable (possibly already attached)")
		}
		cancel()
		cmd.Wait()
	}()
	select {
	case err = <-ready:
	case <-setup.Done():
		err = setup.Err()
	}
	if err != nil {
		cancel()
		<-done
		return nil, err
	}
	return &sizeLock{cancel: cancel, done: done}, nil
}

func (s *Sizing) paneExists(ctx context.Context, pane string) (bool, error) {
	var result struct {
		Panes []struct {
			ID string `json:"pane_id"`
		} `json:"panes"`
	}
	if err := s.bound.api(ctx, "pane.list", map[string]any{}, &result); err != nil {
		return false, err
	}
	for _, p := range result.Panes {
		if p.ID == pane {
			return true, nil
		}
	}
	return false, nil
}

// Caller owns v.mu. Acquisitions precede releases: a competing viewer never
// sees a gap in ownership. The session mutex serializes first arrivals.
func (v *sizeView) reconcile() error {
	s := v.sizing
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[string]bool{}
	if v.active {
		tabs := map[string]bool{v.snapshot.Tab: true}
		for _, n := range v.pending {
			if time.Since(n.since) > 10*time.Second {
				return errors.New("shared tab navigation timed out")
			}
			tabs[n.tab] = true
		}
		for _, p := range v.snapshot.Panes {
			if tabs[p.Tab] {
				wanted[p.ID] = true
			}
		}
	}
	for pane := range wanted {
		if l := s.locks[pane]; l != nil {
			select {
			case <-l.done:
				exists, err := s.paneExists(v.ctx, pane)
				if err != nil {
					return err
				}
				if !exists {
					delete(wanted, pane)
					continue
				}
				return errors.New("shared terminal size controller disconnected")
			default:
			}
		}
		if v.held[pane] {
			continue
		}
		l := s.locks[pane]
		if l == nil {
			if len(s.locks) >= 32 {
				return errors.New("shared terminal sizing limit reached (32 panes)")
			}
			var err error
			l, err = s.bound.holdSize(v.ctx, pane)
			if err != nil {
				exists, queryErr := s.paneExists(v.ctx, pane)
				if queryErr == nil && !exists {
					delete(wanted, pane)
					continue
				}
				return fmt.Errorf("hold shared pane size: %w", err)
			}
			s.locks[pane] = l
		}
		l.refs++
		v.held[pane] = true
	}
	for pane := range v.held {
		if wanted[pane] {
			continue
		}
		l := s.locks[pane]
		l.refs--
		if l.refs == 0 {
			l.cancel()
			<-l.done
			delete(s.locks, pane)
		}
		delete(v.held, pane)
	}
	return nil
}
func (v *sizeView) close() { v.mu.Lock(); defer v.mu.Unlock(); v.active = false; _ = v.reconcile() }

// Retain both old and requested tab locks until a successful response and the
// matching snapshot (or a newer projection barrier) arrive. The two stream
// directions may otherwise race.
func (v *sizeView) settleNavigation() {
	for id, n := range v.pending {
		if n.ack && ((n.floor > 0 && v.snapshot.Revision >= n.floor) || v.snapshot.Tab == n.tab) {
			delete(v.pending, id)
		}
	}
}
func (v *sizeView) surfaceRequest(id string, active bool) error {
	request, _ := json.Marshal(map[string]any{"id": id, "method": "client_shell.surface.set", "params": map[string]bool{"active": active}})
	frame := append([]byte{15}, wireText(v.snapshot.Boot)...)
	frame = append(frame, wireText(string(request))...)
	return v.sendUp(frame)
}
func (v *sizeView) clientFrame(p []byte) ([]byte, error) {
	if len(p) > 0 && p[0] == 15 && v.firstReady != nil {
		select {
		case <-v.firstReady:
		case <-v.ctx.Done():
			return nil, v.ctx.Err()
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	d := wireDecoder{data: p}
	tag := d.number()
	if tag == 20 {
		kind, data := d.text(), d.text()
		if d.err != nil {
			return nil, d.err
		}
		if kind == "endpoint.hello.v1" {
			if v.initialized {
				return nil, errors.New("duplicate endpoint hello")
			}
			var hello map[string]any
			if err := json.Unmarshal([]byte(data), &hello); err != nil {
				return nil, err
			}
			if hello["generation"] != float64(1) {
				return nil, errors.New("unsupported Herdr endpoint generation")
			}
			v.active = hello["surface_active"] != false
			v.requestedActive = v.active
			v.initial = true
			v.initialized = true
			// Let Herdr select its actual initial tab without acquiring geometry. Once
			// its snapshot arrives we pin that tab, then activate this shell internally.
			hello["surface_active"] = false
			dataBytes, _ := json.Marshal(hello)
			p = wireControl(kind, string(dataBytes))
		}
	} else if tag == 15 {
		boot, data := d.text(), d.text()
		if d.err != nil {
			return nil, d.err
		}
		if v.snapshot.Boot != "" && boot != v.snapshot.Boot {
			return p, nil
		}
		var request struct {
			ID     string `json:"id"`
			Method string `json:"method"`
			Params struct {
				Active    *bool  `json:"active"`
				Pane      string `json:"pane_id"`
				Tab       string `json:"tab_id"`
				Workspace string `json:"workspace_id"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(data), &request); err != nil {
			return nil, err
		}
		if strings.HasPrefix(request.ID, "bee-sizing:") {
			return nil, errors.New("reserved sizing request id")
		}
		target := ""
		switch request.Method {
		case "client_shell.surface.set":
			if request.Params.Active != nil {
				if len(v.hide) >= 64 {
					return nil, errors.New("too many pending surface operations")
				}
				v.activity++
				v.requestedActive = *request.Params.Active
				if *request.Params.Active {
					v.active = true
				} else {
					if v.hide == nil {
						v.hide = map[string]uint64{}
					}
					v.hide[request.ID] = v.activity
				}
			}
		case "tab.focus":
			target = request.Params.Tab
		case "pane.focus":
			for _, p := range v.snapshot.Panes {
				if p.ID == request.Params.Pane {
					target = p.Tab
					break
				}
			}
		case "workspace.focus":
			for _, w := range v.snapshot.Workspaces {
				if w.ID == request.Params.Workspace {
					target = w.Tab
					break
				}
			}
		}
		if target != "" {
			if len(v.pending) >= 64 {
				return nil, errors.New("too many pending tab navigations")
			}
			if v.pending == nil {
				v.pending = map[string]*navigation{}
			}
			if v.pending[request.ID] != nil {
				return nil, errors.New("duplicate tab navigation id")
			}
			v.sequence++
			v.pending[request.ID] = &navigation{tab: target, seq: v.sequence, since: time.Now()}
		}
	}
	if err := v.reconcile(); err != nil {
		return nil, err
	}
	// Serialize each inspected request with internal surface barriers while still
	// holding v.mu, so a barrier cannot undo a newer hide/show request.
	if v.sendUp != nil {
		if err := v.sendUp(p); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return p, nil
}
func (v *sizeView) serverFrame(p []byte) ([]byte, error) {
	d := wireDecoder{data: p}
	tag := d.number()
	if tag == 18 {
		boot, id := d.text(), d.text()
		final := d.take(1)
		data := d.text()
		if d.err != nil || len(final) != 1 {
			return nil, errSizingWire
		}
		v.mu.Lock()
		defer v.mu.Unlock()
		if boot != v.snapshot.Boot {
			return p, nil
		}
		if strings.HasPrefix(id, "bee-sizing:") {
			var response struct {
				Error  json.RawMessage `json:"error"`
				Result struct {
					Revision uint64 `json:"projection_revision"`
				} `json:"result"`
			}
			if final[0] != 1 || json.Unmarshal([]byte(data), &response) != nil || len(response.Error) > 0 {
				return nil, errors.New("Herdr rejected shared surface barrier")
			}
			if strings.HasPrefix(id, "bee-sizing:nav:") {
				for _, n := range v.pending {
					if id == "bee-sizing:nav:"+strconv.FormatUint(n.seq, 10) {
						n.floor = response.Result.Revision
					}
				}
				v.settleNavigation()
			}
			return nil, v.reconcile()
		}
		if final[0] == 1 {
			var response struct {
				Error json.RawMessage `json:"error"`
			}
			if json.Unmarshal([]byte(data), &response) == nil {
				if n := v.pending[id]; n != nil {
					if len(response.Error) > 0 {
						delete(v.pending, id)
					} else {
						n.ack = true
						if v.requestedActive && v.sendUp != nil {
							if err := v.surfaceRequest("bee-sizing:nav:"+strconv.FormatUint(n.seq, 10), true); err != nil {
								return nil, err
							}
						}
					}
				}
				if generation, ok := v.hide[id]; ok {
					if len(response.Error) == 0 && generation == v.activity {
						v.active = false
						v.pending = nil
					}
					delete(v.hide, id)
				}
				v.settleNavigation()
			}
		}
		return p, v.reconcile()
	}
	if tag != 20 {
		return p, nil
	}
	kind, data := d.text(), d.text()
	if d.err != nil {
		return nil, d.err
	}
	if kind != "shell.snapshot.v1" {
		return p, nil
	}
	var snapshot shellSnapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.snapshot.Boot == snapshot.Boot && snapshot.Revision < v.snapshot.Revision {
		return p, nil
	}
	v.snapshot = snapshot
	v.settleNavigation()
	if err := v.reconcile(); err != nil {
		return nil, err
	}
	if v.initial {
		v.initial = false
		if v.active {
			if err := v.surfaceRequest("bee-sizing:activate", true); err != nil {
				return nil, err
			}
		}
		if v.firstReady != nil {
			close(v.firstReady)
		}
	}
	return p, nil
}
