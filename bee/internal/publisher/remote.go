package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/deepshape-ai/herdr-hive/bee/internal/config"
	"github.com/deepshape-ai/herdr-hive/bee/internal/herdr"
	"golang.org/x/crypto/ssh"
)

// RemoteExitError applies only to bee on. Other Bee subprocess errors keep
// their existing diagnostics and exit-code contract.
type RemoteExitError struct{ Code int }

func (e *RemoteExitError) Error() string { return fmt.Sprintf("Herdr exited with status %d", e.Code) }

// ResolveTarget accepts a stable share ID or an exact display-name/session pair.
// Do not split on '/': display names and session labels may themselves contain it.
func ResolveTarget(shares []Share, name string) (Share, error) {
	var matches []Share
	for _, s := range shares {
		if name == s.ID || name == s.Name+"/"+s.Label {
			matches = append(matches, s)
		}
	}
	if len(matches) != 1 {
		return Share{}, errors.New("target missing or ambiguous; use a share id from bee targets")
	}
	s := matches[0]
	if !s.API || len(s.Generation) != 32 {
		return Share{}, errors.New("target does not advertise remote API support; upgrade Hive and the target Bee")
	}
	return s, nil
}

// RemoteHerdr creates a private invocation-scoped endpoint. Herdr may open many
// sockets (ping, start, get, wait); every one is pinned to this same publication.
// No connection or input is retried, including after an uncertain submission.
func RemoteHerdr(ctx context.Context, c config.Config, target Share, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := herdr.ValidateRemoteCommand(args); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client, cleanup, err := dial(ctx, c, "hive", "SSH-2.0-HerdrBeeAPI")
	if err != nil {
		return err
	}
	defer cleanup()
	// /tmp is deliberately short enough for Unix sockets on both supported OSes.
	dir, err := os.MkdirTemp("/tmp", "bee-on-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "api.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"share": target.ID, "generation": target.Generation})
	// Verify channel/capability availability before starting any local command.
	probe, requests, err := openAPIChannel(client, payload)
	if err != nil {
		return fmt.Errorf("remote API unavailable: %w", err)
	}
	go ssh.DiscardRequests(requests)
	probe.Close()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	stop := context.AfterFunc(ctx, func() { l.Close(); client.Close() })
	defer stop()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			local, err := l.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				local.Close()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				defer local.Close()
				stop := context.AfterFunc(ctx, func() { local.Close() })
				defer stop()
				local.SetReadDeadline(time.Now().Add(10 * time.Second))
				line, err := herdr.ReadAPILine(local, herdr.MaxAPIRequest)
				if err != nil {
					return
				}
				local.SetReadDeadline(time.Time{})
				var req struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				ch, requests, err := openAPIChannel(client, payload)
				if err != nil {
					local.SetWriteDeadline(time.Now().Add(5 * time.Second))
					herdr.APIError(local, req.ID, "bee_target_unavailable", "target disconnected or changed; rediscover with bee targets; do not automatically retry a write")
					return
				}
				defer ch.Close()
				go ssh.DiscardRequests(requests)
				if _, err = ch.Write(line); err != nil {
					return
				}
				// Detect caller departure even while the remote Herdr is waiting.
				disconnected := make(chan struct{})
				go func() { io.Copy(io.Discard, local); ch.Close(); close(disconnected) }()
				io.Copy(local, ch)
				local.Close()
				<-disconnected
			}()
		}
	}()
	cmd := herdr.RemoteCommand(ctx, path, args)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	cancel()
	l.Close()
	client.Close()
	wg.Wait()
	var child *exec.ExitError
	if errors.As(err, &child) {
		code := child.ExitCode()
		if code < 0 {
			code = 128 + int(child.Sys().(syscall.WaitStatus).Signal())
		}
		return &RemoteExitError{Code: code}
	}
	return err
}

func openAPIChannel(client *ssh.Client, payload []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	timer := time.AfterFunc(10*time.Second, func() { client.Close() })
	defer timer.Stop()
	return client.OpenChannel("bee-api-v1", payload)
}
