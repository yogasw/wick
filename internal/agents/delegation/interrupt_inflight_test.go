package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A draining process past its stuck-work timeout stops every delegation it
// still waits on, as Stop would: child killed, waiter released.
func TestInterruptInflightStopsRunningDelegations(t *testing.T) {
	s, r, run := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 1)
	seedDelegation(t, r, "d2", "root-2", entity.DelegationRunning, 1)
	released := map[string]bool{}
	s.trackInflight("d1", func() { released["d1"] = true })
	s.trackInflight("d2", func() { released["d2"] = true })

	if n := s.InterruptInflight(context.Background()); n != 2 {
		t.Fatalf("stopped %d, want 2", n)
	}
	if k := run.killed(); len(k) != 2 {
		t.Fatalf("kills = %v, want two", k)
	}
	// The waiters own the terminal write (see Interrupt); what this must do
	// is kill the children and release every waiter so the drain can end.
	for _, id := range []string{"d1", "d2"} {
		if !released[id] {
			t.Errorf("%s: waiter not released", id)
		}
	}
}

// Nothing in flight: nothing stopped, nothing killed.
func TestInterruptInflightNoopWhenIdle(t *testing.T) {
	s, _, run := newService(t)
	if n := s.InterruptInflight(context.Background()); n != 0 {
		t.Fatalf("stopped %d, want 0", n)
	}
	if k := run.killed(); len(k) != 0 {
		t.Fatalf("kills = %v, want none", k)
	}
}
