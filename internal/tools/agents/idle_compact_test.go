package agents

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

var testIdlePolicy = provider.IdleCompactPolicy{Enabled: true, Idle: 30 * time.Minute, Trigger: provider.IdleCompactPercent, Threshold: 40}

// idleRig drives a compactor over one session whose row the test edits
// between ticks.
type idleRig struct {
	now       time.Time
	row       idleCandidate
	used      int
	pol       provider.IdleCompactPolicy
	compacted int
	c         *providerIdleCompactor
}

func newIdleRig(t *testing.T) *idleRig {
	r := &idleRig{now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), used: 150_000, pol: testIdlePolicy}
	r.row = idleCandidate{SessionID: "s1"}
	r.c = &providerIdleCompactor{
		Sessions: func() []idleCandidate { return []idleCandidate{r.row} },
		Usage:    func(string) (string, int, int, bool) { return "claude", r.used, 200_000, true },
		Policy:   func(string) provider.IdleCompactPolicy { return r.pol },
		Compact:  func(context.Context, string) error { r.compacted++; return nil },
		Now:      func() time.Time { return r.now },
	}
	return r
}

func (r *idleRig) tick(busy, idle bool) {
	r.row.Busy, r.row.Idle = busy, idle
	r.c.Tick(context.Background())
}

// turn runs one turn that ends normally, then lets the process idle out.
func (r *idleRig) turn() {
	r.tick(true, false)
	r.now = r.now.Add(time.Minute)
	r.row.LastActive = r.now
	r.tick(false, true)
	r.now = r.now.Add(31 * time.Minute)
	r.tick(false, false)
}

func TestProviderIdleCompactorAfterNormalTurn(t *testing.T) {
	r := newIdleRig(t)
	r.turn()
	if r.compacted != 1 {
		t.Fatalf("compacted %d, want 1", r.compacted)
	}
	// The compact turn runs and ends: not armed by it.
	r.tick(true, false)
	r.row.LastActive = r.now.Add(time.Second)
	r.tick(false, true)
	r.now = r.now.Add(time.Hour)
	r.tick(false, false)
	if r.compacted != 1 {
		t.Fatalf("compact turn re-armed: compacted %d, want 1", r.compacted)
	}
	// The user comes back: armed again.
	r.turn()
	if r.compacted != 2 {
		t.Fatalf("after new activity compacted %d, want 2", r.compacted)
	}
}

func TestProviderIdleCompactorShortTurnBetweenTicks(t *testing.T) {
	r := newIdleRig(t)
	r.row.LastActive = r.now.Add(-time.Hour)
	r.tick(false, false)
	// A turn too short to be seen running still moves LastActive.
	r.row.LastActive = r.now
	r.tick(false, true)
	r.now = r.now.Add(31 * time.Minute)
	r.tick(false, false)
	if r.compacted != 1 {
		t.Fatalf("compacted %d, want 1", r.compacted)
	}
}

func TestProviderIdleCompactorLeavesAlone(t *testing.T) {
	t.Run("idle before wick started", func(t *testing.T) {
		r := newIdleRig(t)
		r.row.LastActive = r.now.Add(-2 * time.Hour)
		r.tick(false, false)
		r.now = r.now.Add(time.Hour)
		r.tick(false, false)
		if r.compacted != 0 {
			t.Fatal("old session compacted")
		}
	})
	t.Run("stopped mid-turn", func(t *testing.T) {
		r := newIdleRig(t)
		r.tick(true, false)
		r.row.LastActive = r.now
		r.tick(false, false)
		r.now = r.now.Add(time.Hour)
		r.tick(false, false)
		if r.compacted != 0 {
			t.Fatal("stopped session compacted")
		}
	})
	t.Run("sub-agent", func(t *testing.T) {
		r := newIdleRig(t)
		r.row.SubAgent = true
		r.turn()
		if r.compacted != 0 {
			t.Fatal("sub-agent compacted")
		}
	})
	t.Run("too recent", func(t *testing.T) {
		r := newIdleRig(t)
		r.tick(true, false)
		r.row.LastActive = r.now
		r.tick(false, true)
		r.now = r.now.Add(10 * time.Minute)
		r.tick(false, true)
		if r.compacted != 0 {
			t.Fatal("compacted before the idle time")
		}
	})
	t.Run("below threshold", func(t *testing.T) {
		r := newIdleRig(t)
		r.used = 10_000
		r.turn()
		if r.compacted != 0 {
			t.Fatal("compacted below threshold")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		r := newIdleRig(t)
		r.pol = provider.IdleCompactPolicy{}
		r.turn()
		if r.compacted != 0 {
			t.Fatal("compacted while disabled")
		}
	})
}

func TestProviderIdleCompactorCapsPerTick(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ids := []string{"a", "b", "c", "d"}
	busy := true
	n := 0
	c := &providerIdleCompactor{
		Sessions: func() []idleCandidate {
			var rows []idleCandidate
			for _, id := range ids {
				rows = append(rows, idleCandidate{SessionID: id, LastActive: now.Add(-time.Hour), Busy: busy, Idle: !busy})
			}
			return rows
		},
		Usage: func(string) (string, int, int, bool) { return "claude", 150_000, 200_000, true },
		Policy: func(string) provider.IdleCompactPolicy {
			return provider.IdleCompactPolicy{Enabled: true, Idle: time.Minute, Trigger: provider.IdleCompactPercent, Threshold: 40}
		},
		Compact: func(context.Context, string) error { n++; return nil },
		Now:     func() time.Time { return now },
	}
	c.Tick(context.Background())
	busy = false
	c.Tick(context.Background())
	if n != providerIdleCompactPerTick {
		t.Fatalf("first idle tick compacted %d, want %d", n, providerIdleCompactPerTick)
	}
	c.Tick(context.Background())
	if n != 4 {
		t.Fatalf("after two idle ticks compacted %d, want 4", n)
	}
}
