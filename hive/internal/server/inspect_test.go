package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestartControlRejectsOtherOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir, err := os.MkdirTemp("", "hc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() { done <- Control(ctx, path, func() { calls.Add(1); cancel() }) }()
	var c net.Conn
	var e error
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		c, e = net.Dial("unix", path)
		if e == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(c).Encode("kill 1")
	var ok bool
	if e = json.NewDecoder(c).Decode(&ok); e != nil || ok {
		t.Fatalf("unexpected response %v %v", ok, e)
	}
	c.Close()
	if calls.Load() != 0 {
		t.Fatal("unsupported command restarted server")
	}
	c, e = net.Dial("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(c).Encode("restart")
	if e = json.NewDecoder(c).Decode(&ok); e != nil || !ok {
		t.Fatalf("restart %v %v", ok, e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("control server did not exit")
	}
	if calls.Load() != 1 {
		t.Fatal("restart callback not invoked once")
	}
}
