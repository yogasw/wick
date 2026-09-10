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
