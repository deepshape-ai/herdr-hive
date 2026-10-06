package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// This file intentionally uses only the pre-recovery Serve/Source API. Copy it
// alongside existing fixtures to the old gateway for assertion-failing reds.
func TestServeRepeatedOpenFailureDoesNotHotLoopOrDisplaceHealthySource(t *testing.T) {
	healthy := newFixtureOwner(t, "a", "", full("healthy"))
	var attempts atomic.Int32
	firstOpen := make(chan time.Time, 1)
	failed := Source{ID: "b", Name: "failed", Label: "default", Open: func(context.Context) (io.ReadWriteCloser, error) {
		if attempts.Add(1) == 1 {
			firstOpen <- time.Now()
		}
		return nil, io.ErrClosedPipe
	}}
	h := newFixtureViewer(t, fixtureHello(), healthy.source, failed)
	leg := nextFixtureLeg(t, healthy, 3*time.Second)
	h.snapshot(1, 3*time.Second)
	h.surface("healthy")
	h.forbidWithdrawal = true
	var started time.Time
	select {
	case started = <-firstOpen:
	case <-time.After(time.Second):
		t.Fatal("failed source was not attempted")
	}
	// Old behavior opens once every catalog tick. New behavior retries genuine
	// transient failures at 1/2/4 seconds, never resetting at a metadata hello.
	<-time.After(time.Until(started.Add(5500 * time.Millisecond)))
	if count := attempts.Load(); count < 2 || count > 3 {
		t.Fatalf("stable catalog caused %d Open attempts in 5.5s; want 2..3 bounded recovery attempts", count)
	}
	fixtureOperation(h, "still-usable", "workspace.rename", map[string]any{"name": "healthy"})
	r := nextFixtureRequest(t, leg)
	if err := leg.reply(r, "healthy-after-failures"); err != nil {
		t.Fatal(err)
	}
	h.until(3*time.Second, func(p []byte) bool {
		v := fixtureResponseJSON(p)
		return v != nil && v["id"] == "still-usable" && object(v["result"])["marker"] == "healthy-after-failures"
	})
	if err := leg.write(patch(0, 1, 2, "healthy-after-failures")); err != nil {
		t.Fatal(err)
	}
	h.surface("healthy-after-failures")
	if healthy.count.Load() != 1 {
		t.Fatal("recovery reconnected healthy source")
	}
}

func TestServeInvalidNativeCodecDoesNotReopenUnchangedPublication(t *testing.T) {
	healthy := newFixtureOwner(t, "a", "", full("healthy"))
	var attempts atomic.Int32
	bad := Source{ID: "b", Open: func(ctx context.Context) (io.ReadWriteCloser, error) {
		attempts.Add(1)
		gatewayConn, nativeConn := net.Pipe()
		go func() {
			defer nativeConn.Close()
			go func() {
				<-ctx.Done()
				nativeConn.Close()
			}()
			if _, err := Read(nativeConn); err != nil {
				return
			}
			welcome, _ := json.Marshal(fixtureWelcome())
			if Write(nativeConn, control("endpoint.welcome.v1", welcome)) != nil {
				return
			}
			// This codec was never negotiated. The viewer must quarantine just
			// this publication rather than churn native connects or disconnect.
			Write(nativeConn, control("shell.surface.unknown.v1", []byte("opaque")))
		}()
		return gatewayConn, nil
	}}
	h := newFixtureViewer(t, fixtureHello(), healthy.source, bad)
	leg := nextFixtureLeg(t, healthy, 3*time.Second)
	h.snapshot(1, 3*time.Second)
	h.surface("healthy")
	h.forbidWithdrawal = true
	<-time.After(2500 * time.Millisecond)
	if count := attempts.Load(); count != 1 {
		t.Fatalf("persistent native codec failure reopened %d times; want one attempt", count)
	}
	if err := leg.write(patch(0, 1, 2, "healthy-after-quarantine")); err != nil {
		t.Fatal(err)
	}
	h.surface("healthy-after-quarantine")
	if healthy.count.Load() != 1 {
		t.Fatal("persistent failure displaced healthy source")
	}
}
