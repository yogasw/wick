//go:build !windows

package store

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/storage"
)

// TestTurnHoldsInflightLock: from its first inflight frame until Done, a
// turn holds the session's turn lock, so another holder (a successor's
// boot recovery) cannot take it.
func TestTurnHoldsInflightLock(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	if g, ok := TryLockInflight(layout, "S1"); !ok {
		t.Fatal("lock taken before any turn")
	} else {
		g.Release()
	}
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "hi"})
	if _, ok := TryLockInflight(layout, "S1"); ok {
		t.Fatal("lock free while the turn streams")
	}
	st.Apply(event.AgentEvent{Type: event.Done})
	g, ok := TryLockInflight(layout, "S1")
	if !ok {
		t.Fatal("lock still held after Done")
	}
	g.Release()

	// Flush (a stop) ends the turn too.
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "again"})
	_ = st.Flush()
	if g, ok := TryLockInflight(layout, "S1"); !ok {
		t.Fatal("lock still held after Flush")
	} else {
		g.Release()
	}
}

// TestRecoveredTurnReplacedByFinal is the race the lock normally rules
// out: a recovery folds a turn that was still alive. The partial is filed
// under the id the turn reserved, and the finished turn replaces it, so
// the history keeps one turn per turn.
func TestRecoveredTurnReplacedByFinal(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "part one"})
	recovered, err := RecoverInflight(layout, "S1", "backend", "", nil)
	if err != nil || !recovered {
		t.Fatalf("recover: %v %v", recovered, err)
	}
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: " part two"})
	st.Apply(event.AgentEvent{Type: event.Done})

	lines := readConvLines(t, layout)
	if len(lines) != 2 {
		t.Fatalf("turns: %d", len(lines))
	}
	ghost, final := lines[0], lines[1]
	if !ghost.Interrupted || ghost.TurnID == "" {
		t.Fatalf("recovered turn: %+v", ghost)
	}
	if final.Interrupted || final.Replaces != ghost.TurnID {
		t.Fatalf("final turn does not replace the recovered one: replaces=%q ghost=%q", final.Replaces, ghost.TurnID)
	}
	if final.Text != "part one part two" {
		t.Fatalf("final text = %q", final.Text)
	}
}

// TestFinalTurnWithoutRecoveryReplacesNothing guards the common path.
func TestFinalTurnWithoutRecoveryReplacesNothing(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "a"})
	st.Apply(event.AgentEvent{Type: event.Compaction, Compaction: &event.CompactionInfo{}})
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "b"})
	st.Apply(event.AgentEvent{Type: event.Done})
	for _, l := range readConvLines(t, layout) {
		if l.Replaces != "" {
			t.Fatalf("turn %q replaces %q with no recovery", l.TurnID, l.Replaces)
		}
	}
}

// TestFreshLockRecoversLeftover: a turn that gets the lock fresh while a
// dead writer's inflight.jsonl is still there folds that leftover first
// instead of streaming on top of it.
func TestFreshLockRecoversLeftover(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	if err := storage.AppendJSONL(layout.SessionInflight("S1"), "wick-inflight-v1", "S1",
		InflightEntry{Type: "text_delta", Text: "orphan", At: time.Now().UTC(), TurnID: "7"}); err != nil {
		t.Fatal(err)
	}
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "new"})
	st.Apply(event.AgentEvent{Type: event.Done})
	lines := readConvLines(t, layout)
	if len(lines) != 2 {
		t.Fatalf("turns: %d", len(lines))
	}
	if lines[0].Text != "orphan" || !lines[0].Interrupted || lines[0].TurnID != "7" {
		t.Fatalf("leftover: %+v", lines[0])
	}
	if lines[1].Text != "new" || lines[1].Interrupted || lines[1].Replaces != "" {
		t.Fatalf("new turn: %+v", lines[1])
	}
}
