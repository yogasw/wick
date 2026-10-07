package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/resourceguard"
)

func TestQueueReason(t *testing.T) {
	t0 := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	hang := resourceguard.Event{Kind: "near_hang", At: t0.Add(time.Minute), Detail: "host near a hang (CPU 100% busy)"}

	if r := queueReason(0, true, nil, 80, 3, 3, t0); r != nil {
		t.Fatalf("an empty queue has no reason, got %+v", r)
	}
	r := queueReason(2, true, []resourceguard.Event{{Kind: "resolved"}, hang}, 80, 1, 3, t0)
	if r.Kind != "guard_hold" || r.Detail != hang.Detail || !r.Since.Equal(hang.At) || r.SafePct != 80 {
		t.Fatalf("guard hold = %+v", r)
	}
	// A near_hang already closed by resolved is not this hold's cause.
	r = queueReason(1, true, []resourceguard.Event{hang, {Kind: "resolved"}}, 80, 1, 3, t0)
	if r.Detail != "" || !r.Since.Equal(t0) {
		t.Fatalf("stale near_hang used: %+v", r)
	}
	// The guard wins over full slots: freeing a slot would not start anything.
	if r := queueReason(1, true, nil, 80, 3, 3, t0); r.Kind != "guard_hold" {
		t.Fatalf("kind = %s, want guard_hold", r.Kind)
	}
	if r := queueReason(1, false, nil, 80, 3, 3, t0); r.Kind != "slots_full" || !r.Since.Equal(t0) {
		t.Fatalf("full pool = %+v", r)
	}
	if r := queueReason(1, false, nil, 80, 1, 3, t0); r.Kind != "waiting" {
		t.Fatalf("free slots = %+v", r)
	}
}
