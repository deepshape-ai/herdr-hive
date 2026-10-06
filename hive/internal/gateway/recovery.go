package gateway

import (
	"errors"
	"time"
)

// UpstreamFailure describes a publisher failure without exposing native content.
// A non-retryable failure quarantines this publication generation for this viewer.
type UpstreamFailure struct {
	Code      string
	Retryable bool
}

func (e *UpstreamFailure) Error() string { return "upstream failure: " + e.Code }

// upstreamError distinguishes native lifecycle failures from downstream write
// errors returned through server/publish. Only the former retire a source.
type upstreamError struct{ error }

func (e *upstreamError) Unwrap() error { return e.error }

func protocolFailure() error {
	return &UpstreamFailure{Code: "native_protocol", Retryable: false}
}

const recoveryStableInterval = 10 * time.Second

// Records outlive individual connections, but never the visible publication.
type recoveryRecord struct {
	generation string
	attempts   uint8
	retryAt    time.Time
	blocked    bool
}

func (g *session) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

func (g *session) fail(b *backend, err error) {
	if b == nil || g.sources[b.source.ID] != b {
		return
	}
	now := g.now()
	g.resetRecovery(b, now)
	if g.recovery == nil {
		g.recovery = make(map[string]*recoveryRecord)
	}
	r := g.recovery[b.source.ID]
	if r == nil || r.generation != b.source.Generation {
		r = &recoveryRecord{generation: b.source.Generation}
		g.recovery[b.source.ID] = r
	}
	var failure *UpstreamFailure
	r.blocked = errors.Is(err, errWire) || (errors.As(err, &failure) && !failure.Retryable)
	if !r.blocked {
		if r.attempts < 5 {
			r.attempts++
		}
		r.retryAt = now.Add(time.Second << (r.attempts - 1))
	}
	// Record before removal: immediate publish/refresh cannot forget the cause.
	g.remove(b)
}

func (g *session) renderProgress(b *backend) {
	if !b.surfaceActive || !b.renderReady || b.snapshot == nil {
		return
	}
	for id, target := range g.pending {
		if target == b {
			if _, ordinary := g.requestIDs[id]; !ordinary {
				return
			}
		}
	}
	if b.stableSince.IsZero() {
		b.stableSince = g.now()
	}
}

func (g *session) resetRecovery(b *backend, now time.Time) {
	if !b.stableSince.IsZero() && now.Sub(b.stableSince) >= recoveryStableInterval {
		delete(g.recovery, b.source.ID)
	}
}
