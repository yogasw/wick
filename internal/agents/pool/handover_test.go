package pool

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// entryAt builds an active entry whose state machine runs on a fixed clock, so
// the lifecycle it ends up in is exact instead of racing the test's own clock.
func entryAt(sessID string, at time.Time, evs ...event.EventType) *runEntry {
	m := state.New(func() time.Time { return at })
	for _, t := range evs {
		m.Apply(event.AgentEvent{Type: t})
	}
	return &runEntry{sessID: sessID, state: m}
}

// TestHandoverBlockers locks what the pool contributes to a graceful upgrade:
// turns that are PRODUCING hold the handover, subprocesses that merely exist
// do not. The quiet window after the last one is the drain's job, not this.
func TestHandoverBlockers(t *testing.T) {
	long := time.Now().Add(-2 * time.Hour)

	t.Run("working turn blocks however long it runs", func(t *testing.T) {
		// Producing since two hours ago and still a blocker: the wait is on
		// the work, so a three-hour agent is waited for three hours.
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", long, event.ToolUse)}}
		if got := p.HandoverBlockers(); len(got) != 1 || got[0] != "sess-a" {
			t.Fatalf("want sess-a to block, got %v", got)
		}
	})

	t.Run("spawning turn blocks", func(t *testing.T) {
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", long)}}
		if got := p.HandoverBlockerCount(); got != 1 {
			t.Fatalf("want spawning to block, got %d", got)
		}
	})

	t.Run("idle subprocess does not block", func(t *testing.T) {
		// The old drain counted this — a live-but-idle subprocess waiting out
		// its auto-kill countdown — which is why it needed a deadline to ever
		// finish. It holds no turn: the next message spawns in the successor.
		p := &Pool{active: map[string]*runEntry{
			"a": entryAt("sess-a", long, event.ToolUse, event.Done),
		}}
		if got := p.HandoverBlockers(); len(got) != 0 {
			t.Fatalf("want settled, got %v", got)
		}
	})

	t.Run("an error ends a turn as surely as done", func(t *testing.T) {
		p := &Pool{active: map[string]*runEntry{
			"a": entryAt("sess-a", long, event.ToolUse, event.Error),
		}}
		if got := p.HandoverBlockerCount(); got != 0 {
			t.Fatalf("want settled, got %d", got)
		}
	})

	t.Run("empty pool is settled", func(t *testing.T) {
		p := &Pool{active: map[string]*runEntry{}}
		if got := p.HandoverBlockers(); len(got) != 0 {
			t.Fatalf("want an untouched pool to hand over at once, got %v", got)
		}
	})

	t.Run("only the working ones are named", func(t *testing.T) {
		p := &Pool{active: map[string]*runEntry{
			"a": entryAt("sess-a", long, event.ToolUse),
			"b": entryAt("sess-b", long, event.ToolUse, event.Done),
			"c": entryAt("sess-c", long, event.TextDelta),
		}}
		got := p.HandoverBlockers()
		if len(got) != 2 || got[0] != "sess-a" || got[1] != "sess-c" {
			t.Fatalf("want [sess-a sess-c], got %v", got)
		}
	})
}

// stubLiveness replaces the handover's liveness probes for one test: every
// entry reports pid/attached, and alive answers for that pid.
func stubLiveness(t *testing.T, pid int, attached, alive bool) {
	t.Helper()
	origProc, origAlive, origEnded := turnProcess, processAlive, transportEnded
	turnProcess = func(*runEntry) (int, bool) { return pid, attached }
	processAlive = func(int) bool { return alive }
	transportEnded = func(*runEntry) bool { return false }
	t.Cleanup(func() { turnProcess, processAlive, transportEnded = origProc, origAlive, origEnded })
}

// TestHandoverBlockersGhost locks the fix for a drain that never finished: a
// lifecycle stuck at working/spawning with no process behind it must not hold
// the handover, while real work still does.
func TestHandoverBlockersGhost(t *testing.T) {
	now := time.Now()
	young := now.Add(-10 * time.Second)
	old := now.Add(-30 * time.Minute)

	t.Run("working with a dead process does not block", func(t *testing.T) {
		stubLiveness(t, 4242, true, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", old, event.ToolUse)}}
		if got := p.HandoverBlockers(); len(got) != 0 {
			t.Fatalf("want ghost skipped, got %v", got)
		}
		if !p.ghostWarned["a"] {
			t.Fatal("want the ghost remembered so its WARN is logged once")
		}
		// Polled again (the drain ticks): still skipped, still one entry.
		if got := p.HandoverBlockerCount(); got != 0 || len(p.ghostWarned) != 1 {
			t.Fatalf("want 0 blockers / 1 warned on re-poll, got %d / %d", got, len(p.ghostWarned))
		}
	})

	t.Run("dead pid with a fresh event blocks (respawn in flight)", func(t *testing.T) {
		// A respawn reports the exited pid until the new process is
		// attached; the new turn's events are already arriving.
		stubLiveness(t, 4242, true, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", young, event.ToolUse)}}
		if got := p.HandoverBlockers(); len(got) != 1 || got[0] != "sess-a" {
			t.Fatalf("want sess-a to block, got %v", got)
		}
	})

	t.Run("working with a live process blocks", func(t *testing.T) {
		stubLiveness(t, 4242, true, true)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", old, event.ToolUse)}}
		if got := p.HandoverBlockers(); len(got) != 1 || got[0] != "sess-a" {
			t.Fatalf("want sess-a to block, got %v", got)
		}
	})

	t.Run("working on a pidless transport blocks", func(t *testing.T) {
		// opencode shared server / omp RPC / wick: attached, pid 0, alive.
		stubLiveness(t, 0, true, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", young, event.ToolUse)}}
		if got := p.HandoverBlockerCount(); got != 1 {
			t.Fatalf("want pidless turn to block, got %d", got)
		}
	})

	t.Run("pidless turn whose transport ended does not block", func(t *testing.T) {
		// An opencode turn stuck at working after its stream was gone used
		// to hold the old process open indefinitely.
		stubLiveness(t, 0, true, false)
		transportEnded = func(*runEntry) bool { return true }
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", young, event.ToolUse)}}
		if got := p.HandoverBlockerCount(); got != 0 {
			t.Fatalf("want ended pidless turn skipped, got %d", got)
		}
	})

	t.Run("young spawning without a pid blocks", func(t *testing.T) {
		stubLiveness(t, 0, false, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", young)}}
		if got := p.HandoverBlockers(); len(got) != 1 || got[0] != "sess-a" {
			t.Fatalf("want young spawn to block, got %v", got)
		}
	})

	t.Run("old spawning without a pid does not block", func(t *testing.T) {
		stubLiveness(t, 0, false, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", old)}}
		if got := p.HandoverBlockers(); len(got) != 0 {
			t.Fatalf("want stale spawn skipped, got %v", got)
		}
	})

	t.Run("warned set forgets entries that left the pool", func(t *testing.T) {
		stubLiveness(t, 4242, true, false)
		p := &Pool{active: map[string]*runEntry{"a": entryAt("sess-a", old, event.ToolUse)}}
		p.HandoverBlockers()
		delete(p.active, "a")
		p.HandoverBlockers()
		if len(p.ghostWarned) != 0 {
			t.Fatalf("want warned set pruned, got %v", p.ghostWarned)
		}
	})
}

func TestGhostTurn(t *testing.T) {
	dead := func(int) bool { return false }
	live := func(int) bool { return true }
	cases := []struct {
		name     string
		lc       state.Lifecycle
		pid      int
		attached bool
		ended    bool
		age      time.Duration
		probe    func(int) bool
		want     bool
	}{
		{"idle is never a ghost", state.LifecycleIdle, 7, true, false, time.Hour, dead, false},
		{"working dead pid, quiet", state.LifecycleWorking, 7, true, false, ghostDeadGrace, dead, true},
		{"working dead pid, fresh event (respawn in flight)", state.LifecycleWorking, 7, true, false, time.Second, dead, false},
		{"working live pid", state.LifecycleWorking, 7, true, false, time.Hour, live, false},
		{"working pidless attached, streaming", state.LifecycleWorking, 0, true, false, time.Minute, dead, false},
		{"working pidless attached, transport ended", state.LifecycleWorking, 0, true, true, time.Second, dead, true},
		{"working pidless attached, long silence still waits", state.LifecycleWorking, 0, true, false, 3 * time.Hour, dead, false},
		{"spawning nothing attached young", state.LifecycleSpawning, 0, false, false, time.Minute, dead, false},
		{"spawning nothing attached old", state.LifecycleSpawning, 0, false, false, ghostSpawnGrace, dead, true},
	}
	for _, c := range cases {
		if got := ghostTurn(c.lc, c.pid, c.attached, c.ended, c.age, c.probe); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
