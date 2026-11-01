package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A message for a session whose turn the draining previous build still runs
// is held, not spawned beside it, and delivered once that turn ends. A second
// message joins the same wait and goes out in the same spawn.
func TestSendWaitsForPredecessorTurn(t *testing.T) {
	orig := predecessorPoll
	predecessorPoll = 20 * time.Millisecond
	t.Cleanup(func() { predecessorPoll = orig })

	sp := &scriptedSpawner{}
	p, _ := newPool(t, 1, sp)
	var holds atomic.Bool
	holds.Store(true)
	p.cfg.PredecessorHolds = func(string) bool { return holds.Load() }

	ctx := context.Background()
	if err := p.Send(ctx, "S", "default", "slack", "user", "first"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(ctx, "S", "default", "slack", "user", "second"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := sp.callsSnapshot(); n != 0 {
		t.Fatalf("spawned %d while the predecessor still runs the turn", n)
	}

	holds.Store(false)
	waitFor(t, func() bool { return sp.callsSnapshot() == 1 }, 2*time.Second)
	time.Sleep(100 * time.Millisecond)
	if n := sp.callsSnapshot(); n != 1 {
		t.Fatalf("spawns = %d, want exactly 1", n)
	}
}

// Without a predecessor the hold never applies.
func TestSendWithoutPredecessorSpawnsAtOnce(t *testing.T) {
	sp := &scriptedSpawner{}
	p, _ := newPool(t, 1, sp)
	p.cfg.PredecessorHolds = func(string) bool { return false }
	if err := p.Send(context.Background(), "S", "default", "slack", "user", "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() == 1 }, 2*time.Second)
}
