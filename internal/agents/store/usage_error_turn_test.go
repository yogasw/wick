package store

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// TestErrorTurnRecordsItsSpend: an errored turn that reported what it
// spent lands in the ledger once, like a Done. The usage-less Error wick
// adds when the process exits records nothing more, and the next turn
// adds only its own flows.
func TestErrorTurnRecordsItsSpend(t *testing.T) {
	l := usageLayout(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := &Store{layout: l, sessionID: "s1", provider: "claude/me", now: func() time.Time { return at }}
	load := func() *ProviderUsage {
		su, err := LoadSessionUsage(l, "s1")
		if err != nil {
			t.Fatal(err)
		}
		return su.Providers["claude/me"]
	}

	if _, err := s.Apply(event.AgentEvent{ContextUsed: 50_000}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "error_max_turns", Usage: &event.TokenUsage{
		Input: 200, Output: 200, CacheRead: 4000, CostUSD: 2, ContextUsed: 50_000, Model: "claude-opus-5",
	}}); err != nil {
		t.Fatal(err)
	}
	p := load()
	if p.Turns != 1 || p.Output != 200 || p.CacheRead != 4000 || len(p.Series) != 1 || p.Series[0].ContextUsed != 50_000 {
		t.Fatalf("error turn: %+v series=%+v", p.UsageTotals, p.Series)
	}

	// The process then exits: wick's own Error carries no usage.
	if _, err := s.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "process exited"}); err != nil {
		t.Fatal(err)
	}
	if p := load(); p.Turns != 1 || len(p.Series) != 1 {
		t.Fatalf("exit error recorded again: turns=%d series=%+v", p.Turns, p.Series)
	}

	if _, err := s.Apply(event.AgentEvent{Type: event.Done, Usage: &event.TokenUsage{
		Input: 10, Output: 20, CacheRead: 1000, CostUSD: 0.25, ContextUsed: 51_000, Model: "claude-opus-5",
	}}); err != nil {
		t.Fatal(err)
	}
	p = load()
	if p.Turns != 2 || p.Output != 220 || p.CacheRead != 5000 || len(p.Series) != 2 {
		t.Fatalf("next turn: %+v turns=%d series=%d", p.UsageTotals, p.Turns, len(p.Series))
	}
	if d := p.CostUSD - 2.25; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost: want 2.25, got %v", p.CostUSD)
	}
}
