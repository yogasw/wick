package provider

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// An account that keeps failing with no reset time stays out until one
// recheck interval passes without a failure, not one interval after the
// first failure — else every message goes back to it while it is down.
func TestRepeatFailureRestartsTheWait(t *testing.T) {
	healthTestDirs(t)
	t0 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	every := 10 * time.Minute
	MarkUnhealthy("k", Health{Kind: event.ErrUnavailable, Since: t0, CheckedAt: t0, Recheck: every})
	if ok, _ := HealthyNow("k", t0.Add(11*time.Minute)); !ok {
		t.Fatal("not tentatively healthy one interval after a single failure")
	}
	t1 := t0.Add(11 * time.Minute)
	MarkUnhealthy("k", Health{Kind: event.ErrUnavailable, Since: t1, CheckedAt: t1, Recheck: every})
	h, _ := HealthOf("k")
	if !h.Since.Equal(t0) {
		t.Fatalf("Since = %v, the first failure must stay", h.Since)
	}
	if ok, _ := HealthyNow("k", t0.Add(12*time.Minute)); ok {
		t.Fatal("healthy right after a repeat failure")
	}
	if ok, _ := HealthyNow("k", t1.Add(every)); !ok {
		t.Fatal("not tentatively healthy one interval after the last failure")
	}
}
