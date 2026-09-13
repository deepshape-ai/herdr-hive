package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type panelPane struct {
	ID       string `json:"pane_id"`
	Terminal string `json:"terminal_id"`
	Tab      string `json:"tab_id"`
}
type panelRPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *panelRPCError) Error() string { return e.Code + ": " + e.Message }

func panelRPC(ctx context.Context, socket, method string, params, result any) error {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(6 * time.Second))
	if err = json.NewEncoder(conn).Encode(map[string]any{"id": "bee-panel", "method": method, "params": params}); err != nil {
		return err
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *panelRPCError  `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&response); err != nil {
		return err
	}
	if response.Error != nil {
		return response.Error
	}
	return json.Unmarshal(response.Result, result)
}

// OpenSettings scopes one pane to the invoking tab. Both pane and terminal IDs
// are checked before focusing or closing a remembered pane after a server restart.
func OpenSettings(ctx context.Context, dir string) (string, error) {
	socket := os.Getenv("HERDR_SOCKET_PATH")
	if socket == "" {
		return "", errors.New("open Bee from inside Herdr")
	}
	var invocation struct {
		Focused string `json:"focused_pane_id"`
	}
	if raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &invocation); err != nil {
			return "", err
		}
	}
	var current struct {
		Pane panelPane `json:"pane"`
	}
	method, params := "pane.current", map[string]any{"caller_pane_id": os.Getenv("HERDR_PANE_ID")}
	if invocation.Focused != "" {
		method, params = "pane.get", map[string]any{"pane_id": invocation.Focused}
	}
	if err := panelRPC(ctx, socket, method, params, &current); err != nil {
		return "", err
	}
	if current.Pane.ID == "" || current.Pane.Tab == "" {
		return "", errors.New("Herdr returned no current pane")
	}
	folder := filepath.Join(dir, "panels")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(socket+"\x00"+current.Pane.Tab)))
	path := filepath.Join(folder, key+".json")
	lock, err := os.OpenFile(filepath.Join(folder, "open.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return "", errors.New("Bee pane is already opening; try again")
	}
	var remembered panelPane
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &remembered)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if remembered.ID != "" {
		var existing struct {
			Pane panelPane `json:"pane"`
		}
		err := panelRPC(ctx, socket, "pane.get", map[string]any{"pane_id": remembered.ID}, &existing)
		if err != nil {
			var remote *panelRPCError
			if !errors.As(err, &remote) || remote.Code != "pane_not_found" {
				return "", err
			}
		} else if existing.Pane.Terminal == remembered.Terminal && existing.Pane.Tab == current.Pane.Tab {
			action := "focus"
			if existing.Pane.ID == current.Pane.ID {
				action = "close"
			}
			var result any
			if err = panelRPC(ctx, socket, "plugin.pane."+action, map[string]any{"pane_id": existing.Pane.ID}, &result); err != nil {
				return "", err
			}
			if action == "close" {
				err = os.Remove(path)
			}
			return action, err
		}
	}
	var opened struct {
		PluginPane struct {
			Pane panelPane `json:"pane"`
		} `json:"plugin_pane"`
	}
	err = panelRPC(ctx, socket, "plugin.pane.open", map[string]any{"plugin_id": "herdr.bee", "entrypoint": "settings", "placement": "split", "direction": "right", "target_pane_id": current.Pane.ID, "focus": true}, &opened)
	if err != nil {
		return "", err
	}
	if opened.PluginPane.Pane.ID == "" || opened.PluginPane.Pane.Terminal == "" {
		return "", errors.New("Herdr returned no plugin pane")
	}
	data, err := json.Marshal(opened.PluginPane.Pane)
	if err != nil {
		return "", err
	}
	// Records are hints only: a partial write is treated as a missing record.
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return "open", nil
}
