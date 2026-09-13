package upgrade

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrackerWaitsUntilZero(t *testing.T) {
	var tr Tracker
	// Atomic, not a plain int: Wait polls InFlight from its own goroutine
	// while this one drops the count, which is exactly the unsynchronised
	// pair -race flags. Every real InFlight reads its count under a lock
	// for the same reason; the fake one has to be safe too.
	var n atomic.Int64
	n.Store(2)
	tr.Add(Work{Name: "turns", InFlight: func() int { return int(n.Load()) }})

	go func() {
		time.Sleep(400 * time.Millisecond)
		n.Store(0)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if left := tr.Wait(ctx); len(left) != 0 {
		t.Fatalf("expected a clean drain, still busy: %v", left)
	}
}

func TestTrackerReportsWhatIsStillBusyOnTimeout(t *testing.T) {
	var tr Tracker
	tr.Add(Work{
		Name:     "workflow runs",
		InFlight: func() int { return 1 },
		Detail:   func() []string { return []string{"run-42"} },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	left := tr.Wait(ctx)
	if len(left) != 1 || left[0] != "workflow runs=1 (run-42)" {
		t.Fatalf("unexpected outstanding report: %#v", left)
	}
}

// Registration happens in constructors, so a second construction must replace
// the first entry — otherwise a test binary (or any process that builds a
// subsystem twice) reports double the work and never drains.
func TestTrackerDedupesByName(t *testing.T) {
	var tr Tracker
	tr.Add(Work{Name: "turns", InFlight: func() int { return 3 }})
	tr.Add(Work{Name: "turns", InFlight: func() int { return 1 }})
	if got := tr.Total(); got != 1 {
		t.Fatalf("Total() = %d, want 1 (second registration should replace the first)", got)
	}
}

func TestBusySkipsIdleSubsystems(t *testing.T) {
	var tr Tracker
	tr.Add(Work{Name: "idle", InFlight: func() int { return 0 }})
	tr.Add(Work{Name: "busy", InFlight: func() int { return 5 }})
	busy := tr.Busy()
	if len(busy) != 1 || busy[0] != "busy=5" {
		t.Fatalf("Busy() = %#v, want [busy=5]", busy)
	}
}

// TestWaitSettled locks the rule a handover switches on: the drain ends when
// the process has STOPPED, not when a timer expires — and "stopped for an
// instant" does not count.
func TestWaitSettled(t *testing.T) {
	t.Run("waits out the quiet window after the last work", func(t *testing.T) {
		tr := &Tracker{}
		// Atomic, not a plain int: WaitSettled polls from this goroutine
		// while the one below mutates.
		var n atomic.Int64
		n.Store(1)
		tr.Add(Work{Name: "workflow runs", InFlight: func() int { return int(n.Load()) }})
		go func() {
			time.Sleep(150 * time.Millisecond)
			n.Store(0)
		}()
		start := time.Now()
		if left := tr.WaitSettled(context.Background(), 300*time.Millisecond); len(left) > 0 {
			t.Fatalf("want settled, still busy: %v", left)
		}
		// Returning before work-end + window would mean handing over inside
		// the gap where a follow-up node or tool result usually lands.
		if d := time.Since(start); d < 400*time.Millisecond {
			t.Fatalf("returned after %s — quiet window not honoured", d)
		}
	})

	t.Run("new work restarts the window", func(t *testing.T) {
		tr := &Tracker{}
		var n atomic.Int64
		tr.Add(Work{Name: "cron jobs", InFlight: func() int { return int(n.Load()) }})
		go func() {
			// A cron job fires while the drain was already quiet: the window
			// must start again, not resume where it left off.
			time.Sleep(100 * time.Millisecond)
			n.Store(1)
			time.Sleep(200 * time.Millisecond)
			n.Store(0)
		}()
		start := time.Now()
		tr.WaitSettled(context.Background(), 250*time.Millisecond)
		if d := time.Since(start); d < 500*time.Millisecond {
			t.Fatalf("returned after %s — window did not restart", d)
		}
	})

	t.Run("never waits for work that is already gone", func(t *testing.T) {
		tr := &Tracker{}
		tr.Add(Work{Name: "agent turns", InFlight: func() int { return 0 }})
		start := time.Now()
		if left := tr.WaitSettled(context.Background(), 50*time.Millisecond); len(left) > 0 {
			t.Fatalf("want settled, got %v", left)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("idle tracker took %s", d)
		}
	})

	t.Run("a cancelled ctx reports what was still running", func(t *testing.T) {
		// This is the forced-swap / hard-cap path: the caller gave up, so the
		// work is about to be interrupted and must be named in the log.
		tr := &Tracker{}
		tr.Add(Work{Name: "workflow runs", InFlight: func() int { return 2 }})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		left := tr.WaitSettled(ctx, time.Second)
		if len(left) != 1 || left[0] != "workflow runs=2" {
			t.Fatalf("want the outstanding work named, got %v", left)
		}
	})
}
