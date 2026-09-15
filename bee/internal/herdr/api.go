package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

const MaxAPIRequest = 1 << 20
const maxAPIResponse = 16 << 20

// The publisher, not the consumer or Hive UI, owns this operation boundary.
// These methods act only on the bound session. No server administration,
// plugin installation, arbitrary socket tunnelling, or binary attachment.
func allowedAPI(method string) bool {
	switch method {
	case "ping", "session.snapshot",
		"agent.list", "agent.get", "agent.read", "agent.explain", "agent.prompt",
		"agent.wait", "agent.send_keys", "agent.start", "agent.rename", "agent.focus",
		"agent.view.set", "agent.view.clear",
		"workspace.list", "workspace.get", "workspace.create", "workspace.rename",
		"workspace.focus", "workspace.move", "workspace.move_block", "workspace.close",
		"tab.list", "tab.get", "tab.create", "tab.rename", "tab.focus", "tab.move", "tab.close",
		"pane.list", "pane.get", "pane.current", "pane.layout", "pane.process_info",
		"pane.neighbor", "pane.edges", "pane.split", "pane.move", "pane.swap",
		"pane.focus", "pane.focus_direction", "pane.resize", "pane.zoom", "pane.rename",
		"pane.close", "pane.read", "pane.send_text", "pane.send_keys", "pane.send_input",
		"pane.input.set", "pane.wait_for_output", "layout.set_split_ratio", "layout.export":
		return true
	}
	return false
}

// ReadAPILine bounds allocations before JSON decoding. One request/response per
// channel matches Herdr's CLI API client, including its separate ping requests.
func ReadAPILine(r io.Reader, limit int64) ([]byte, error) {
	line, err := bufio.NewReader(io.LimitReader(r, limit+1)).ReadBytes('\n')
	if int64(len(line)) > limit {
		return nil, errors.New("Herdr API record exceeds limit")
	}
	if err != nil {
		return nil, err
	}
	return line, nil
}

func APIError(w io.Writer, id, code, message string) error {
	return json.NewEncoder(w).Encode(map[string]any{
		"id": id, "error": map[string]string{"code": code, "message": message},
	})
}

func (b Bound) ServeAPI(ctx context.Context, remote io.ReadWriteCloser) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { remote.Close() })
	defer stop()
	// Bound incomplete requests, while allowing native agent waits to run until
	// their own timeout or caller cancellation after a complete request arrives.
	timer := time.AfterFunc(10*time.Second, func() { remote.Close() })
	line, err := ReadAPILine(remote, MaxAPIRequest)
	timer.Stop()
	if err != nil {
		return err
	}
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &req) != nil || req.ID == "" || len(req.ID) > 256 {
		return APIError(remote, "", "invalid_request", "invalid Herdr API request")
	}
	if !allowedAPI(req.Method) {
		return APIError(remote, req.ID, "bee_operation_unsupported", "operation is not available through Bee")
	}
	// Decode and re-encode the envelope so duplicate JSON keys cannot be
	// interpreted differently by the Go gate and the upstream Herdr parser.
	line, err = json.Marshal(req)
	if err != nil {
		return err
	}
	if err = b.Check(); err != nil {
		return APIError(remote, req.ID, "bee_session_changed", "shared session was replaced; rediscover the target")
	}
	local, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", b.Socket)
	if err != nil {
		return APIError(remote, req.ID, "bee_session_unavailable", "shared Herdr API is unavailable")
	}
	defer local.Close()
	// Close the stat-to-connect race: if connect reached a replacement socket,
	// reject before writing any operation into it.
	if err = b.Check(); err != nil {
		return APIError(remote, req.ID, "bee_session_changed", "shared session was replaced; rediscover the target")
	}
	stopLocal := context.AfterFunc(ctx, func() { local.Close() })
	defer stopLocal()
	// Replacements must also terminate an outstanding wait on the old server.
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if b.Check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	local.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err = local.Write(append(line, '\n')); err != nil {
		return err
	}
	reply, err := ReadAPILine(local, maxAPIResponse)
	if err != nil {
		return err
	}
	// A slow reader must not hold a publisher slot indefinitely.
	timer = time.AfterFunc(15*time.Second, func() { remote.Close() })
	defer timer.Stop()
	_, err = remote.Write(reply)
	return err
}
