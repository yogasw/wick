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
