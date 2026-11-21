package pool

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/state"
)

// TestReapDecision locks the safety rules that keep a long-running process
// from being reaped out from under itself, and that a zombie is only reaped
// after sustained death.
func TestReapDecision(t *testing.T) {
	const dead = false
	const alive = true

	cases := []struct {
		name       string
		lc         state.Lifecycle
		pid        int
		attached   bool
		alive      bool
		probes     int
		wantReap   bool
		wantProbes int
	}{
		// A working turn is never reaped, no matter what the probe says — a
		// long turn must survive. Counter resets.
		{"working+dead never reaped", state.LifecycleWorking, 123, true, dead, 5, false, 0},
		{"spawning+dead never reaped", state.LifecycleSpawning, 123, true, dead, 5, false, 0},
		// omp RPC, opencode and the in-process provider attach a turn with no
		// per-turn OS PID. It must never be mistaken for a dead idle slot.
		{"idle+attached pid0 never reaped", state.LifecycleIdle, 0, true, dead, 5, false, 0},
		// Idle with no attached process (pid 0) is a held slot with nothing
		// behind it — same as a dead pid: accumulate toward reap.
		{"idle+pid0 first tick no reap", state.LifecycleIdle, 0, false, dead, 0, false, 1},
		{"idle+pid0 second tick reaps", state.LifecycleIdle, 0, false, dead, 1, true, 2},
		// Alive idle (e.g. resident codex) → never reaped, reset.
		{"idle+alive left alone", state.LifecycleIdle, 123, true, alive, 1, false, 0},
		// Idle + dead pid: first tick increments but does NOT reap (rides out
		// a transient miss); second consecutive tick reaps.
		{"idle+dead first tick no reap", state.LifecycleIdle, 123, true, dead, 0, false, 1},
		{"idle+dead second tick reaps", state.LifecycleIdle, 123, true, dead, 1, true, 2},
		// A transient alive between dead ticks resets the counter, so a live
		// process can never accumulate to the threshold.
		{"alive resets accumulated probes", state.LifecycleIdle, 123, true, alive, 1, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reap, next := reapDecision(c.lc, c.pid, c.attached, c.alive, c.probes)
			if reap != c.wantReap {
				t.Errorf("reap = %v, want %v", reap, c.wantReap)
			}
			if next != c.wantProbes {
				t.Errorf("nextProbes = %d, want %d", next, c.wantProbes)
			}
		})
	}
}

// TestReapDecisionThreshold: a process that flaps (dead, dead, alive, dead)
// never reaches the reap threshold because the alive tick resets it — the
// core protection for a long-running process that occasionally fails to
// probe.
func TestReapDecisionFlappingNeverReaps(t *testing.T) {
	probes := 0
	seq := []bool{false, true, false, true, false} // dead, alive, dead, alive, dead
	for i, aliveTick := range seq {
		reap, next := reapDecision(state.LifecycleIdle, 123, true, aliveTick, probes)
		probes = next
		if reap {
			t.Fatalf("tick %d: reaped a flapping (alive-in-between) process", i)
		}
	}
}
