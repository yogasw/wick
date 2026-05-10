package connectors

import (
	"fmt"
	"sync"
	"time"
)

// rateLimiter implements a per-connector sliding-window rate limit using
// only stdlib. Each connector instance gets an independent window so a
// noisy connector does not affect others.
//
// The window is 1 minute. Thread-safe for concurrent Execute calls.
// Not distributed — counts are per-process and reset on restart.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time // connectorID → call timestamps in current window
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: make(map[string][]time.Time)}
}

// Allow records a call attempt for connectorID and returns an error when
// the sliding-window count would exceed maxPerMinute. Passing maxPerMinute
// <= 0 always allows the call.
func (r *rateLimiter) Allow(connectorID string, maxPerMinute int) error {
	if maxPerMinute <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	prev := r.windows[connectorID]
	// Compact: drop timestamps outside the 1-minute window.
	valid := prev[:0]
	for _, t := range prev {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= maxPerMinute {
		r.windows[connectorID] = valid
		return fmt.Errorf("rate limit exceeded: connector allows %d calls/min, try again later", maxPerMinute)
	}
	r.windows[connectorID] = append(valid, now)
	return nil
}
