package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"time"
)

// Limits caps SSH sessions globally, including native bootstrap and stream sessions.
func (s *Server) Limits(connections, channels int) {
	s.sem = make(chan struct{}, connections)
	s.channels = make(chan struct{}, channels)
}

type Detail struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Session     string `json:"session"`
	Connections int    `json:"connections"`
	ToPublisher uint64 `json:"bytes_to_publisher"`
	ToConsumer  uint64 `json:"bytes_to_consumer"`
}
type Snapshot struct {
	PID            int      `json:"pid"`
	Version        string   `json:"version"`
	Executable     string   `json:"executable"`
	RegistryBytes  int64    `json:"registry_bytes"`
	UptimeSeconds  int64    `json:"uptime_seconds"`
	Connections    int      `json:"connections"`
	Channels       int      `json:"channels"`
	MaxConnections int      `json:"max_connections"`
	MaxChannels    int      `json:"max_channels"`
	Rejected       uint64   `json:"rejected"`
	HeapBytes      uint64   `json:"heap_bytes"`
	RuntimeBytes   uint64   `json:"runtime_bytes"`
	Goroutines     int      `json:"goroutines"`
	Shares         []Detail `json:"shares"`
}

func (s *Server) Snapshot() Snapshot {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Snapshot{RegistryBytes: s.Registry.DiskBytes(), UptimeSeconds: int64(time.Since(s.started).Seconds()), Connections: len(s.conns), Channels: len(s.channels), MaxConnections: cap(s.sem), MaxChannels: cap(s.channels), Rejected: s.rejected.Load(), HeapBytes: mem.HeapAlloc, RuntimeBytes: mem.Sys, Goroutines: runtime.NumGoroutine(), Shares: []Detail{}}
	for _, b := range s.shares {
		v.Shares = append(v.Shares, Detail{b.share.ID, b.share.Name, b.share.Label, len(b.active), b.up.Load(), b.down.Load()})
	}
	sort.Slice(v.Shares, func(i, j int) bool { return v.Shares[i].ID < v.Shares[j].ID })
	return v
}

// Inspect serves metadata only on a private Unix socket, independently of SSH capacity.
func (s *Server) Inspect(ctx context.Context, path string) error {
	os.Remove(path)
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer l.Close()
	defer os.Remove(path)
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	go func() { <-ctx.Done(); l.Close() }()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		snapshot := s.Snapshot()
		snapshot.PID = os.Getpid()
		snapshot.Version = s.Version
		snapshot.Executable = s.Executable
		json.NewEncoder(c).Encode(snapshot)
		c.Close()
	}
}

// Control requests restart inside the service process. Privileged clients never
// signal a PID supplied by the less-privileged service.
func Control(ctx context.Context, path string, restart func()) error {
	os.Remove(path)
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer l.Close()
	defer os.Remove(path)
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	go func() { <-ctx.Done(); l.Close() }()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		var op string
		e = json.NewDecoder(io.LimitReader(c, 128)).Decode(&op)
		ok := e == nil && op == "restart"
		json.NewEncoder(c).Encode(ok)
		c.Close()
		if ok {
			restart()
			return nil
		}
	}
}
