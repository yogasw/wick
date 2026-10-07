package store

import (
	"github.com/yogasw/wick/internal/agents/event"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
)

// usageLayout is a throwaway data dir for the ledger tests.
func usageLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return l
}

// All-time model totals come from the exact buckets; a window has to be
// rebuilt from the per-turn trail. Both have to answer, and both have to
// agree with the provider figure beside them.
func TestModelTotalsBetween(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	p := &ProviderUsage{}
	p.addModel("claude-opus-5", UsageTotals{Input: 100, Output: 10}, t0)
	p.addModel("claude-haiku-4", UsageTotals{Input: 20, Output: 2}, t0.Add(time.Hour))
	p.addModel("claude-opus-5", UsageTotals{Input: 50, Output: 5}, t0.Add(2*time.Hour))
	p.Series = []UsagePoint{
		{At: t0, Model: "claude-opus-5", Input: 100, Output: 10},
		{At: t0.Add(time.Hour), Model: "claude-haiku-4", Input: 20, Output: 2},
		{At: t0.Add(2 * time.Hour), Model: "claude-opus-5", Input: 50, Output: 5},
	}

	all, turns := p.ModelTotalsBetween(time.Time{}, time.Time{})
	if all["claude-opus-5"].Input != 150 || turns["claude-opus-5"] != 2 {
		t.Fatalf("all-time opus = %+v / %d turns, want 150 input over 2 turns", all["claude-opus-5"], turns["claude-opus-5"])
	}

	// A window that starts after the first turn drops it — and drops the
	// model's turn count with it.
	win, winTurns := p.ModelTotalsBetween(t0.Add(30*time.Minute), time.Time{})
	if win["claude-opus-5"].Input != 50 || winTurns["claude-opus-5"] != 1 {
		t.Fatalf("windowed opus = %+v / %d turns, want only the later turn", win["claude-opus-5"], winTurns["claude-opus-5"])
	}
	if win["claude-haiku-4"].Input != 20 {
		t.Fatalf("windowed haiku = %+v, want the turn inside the window", win["claude-haiku-4"])
	}
}

// A ledger written before models were recorded still has to add up to the
// provider row sitting next to it, rather than showing an empty breakdown.
func TestModelTotalsFallBackToTheProviderBucket(t *testing.T) {
	p := &ProviderUsage{
		UsageTotals: UsageTotals{Input: 80, Output: 8},
		Turns:       4,
		Model:       "claude-opus-5",
	}
	all, turns := p.ModelTotalsBetween(time.Time{}, time.Time{})
	if all["claude-opus-5"].Input != 80 || turns["claude-opus-5"] != 4 {
		t.Fatalf("got %+v / %v, want the provider's own figures under its last model", all, turns)
	}
}

// A turn whose vendor named no model is still spend. It gets a row rather
// than being dropped, or the breakdown would not sum to the total.
func TestModelTotalsBucketTheUnnamed(t *testing.T) {
	p := &ProviderUsage{}
	p.addModel(modelKey(""), UsageTotals{Input: 5}, time.Now())
	all, _ := p.ModelTotalsBetween(time.Time{}, time.Time{})
	if all[UnknownModel].Input != 5 {
		t.Fatalf("got %+v, want the unnamed turn under %q", all, UnknownModel)
	}
}

// The ledger is written when a turn ENDS, which is when the level stops
// being the interesting number. A mid-turn reading updates the level and
// nothing else — flows and turns belong to the turn, and the turn has
// not finished.
func TestRecordContextLevelTouchesOnlyTheLevel(t *testing.T) {
	l := usageLayout(t)
	s := &Store{layout: l, sessionID: "s1", provider: "claude/default", now: time.Now}

	if err := s.recordContextLevel(42_000, time.Now()); err != nil {
		t.Fatal(err)
	}
	su, err := LoadSessionUsage(l, "s1")
	if err != nil {
		t.Fatal(err)
	}
	p := su.Providers["claude/default"]
	if p == nil || p.ContextUsed != 42_000 {
		t.Fatalf("level not recorded: %+v", p)
	}
	if p.Turns != 0 || len(p.Series) != 0 || p.UsageTotals != (UsageTotals{}) {
		t.Fatalf("a mid-turn reading counted a turn or added flows: %+v", p)
	}
	if su.Turns != 0 {
		t.Fatalf("session turns = %d, want 0 until the turn ends", su.Turns)
	}
}

// The level arrives on every frame of a long turn and the whole ledger is
// rewritten each time, so the writes are spaced out.
func TestRecordContextLevelIsThrottled(t *testing.T) {
	l := usageLayout(t)
	s := &Store{layout: l, sessionID: "s1", provider: "claude/default", now: time.Now}
	at := time.Now()

	if err := s.recordContextLevel(10_000, at); err != nil {
		t.Fatal(err)
	}
	if err := s.recordContextLevel(11_000, at.Add(time.Millisecond*200)); err != nil {
		t.Fatal(err)
	}
	su, _ := LoadSessionUsage(l, "s1")
	if got := su.Providers["claude/default"].ContextUsed; got != 10_000 {
		t.Fatalf("level = %d, want the second reading held back by the throttle", got)
	}

	if err := s.recordContextLevel(12_000, at.Add(levelWriteInterval+time.Second)); err != nil {
		t.Fatal(err)
	}
	su, _ = LoadSessionUsage(l, "s1")
	if got := su.Providers["claude/default"].ContextUsed; got != 12_000 {
		t.Fatalf("level = %d, want the reading past the interval to land", got)
	}
}

// A turn stopped mid-way ends with a synthetic Done that carries no usage.
// The level it climbed to must still land in the series, or the history
// draws a flat line through the climb (session 292e2e51: 24k → 107k, then
// Stop, and the chart showed neither).
func TestStoppedTurnPlotsItsLastLevel(t *testing.T) {
	l := usageLayout(t)
	s := &Store{layout: l, sessionID: "s1", provider: "omp/yoga", now: time.Now}
	for _, lvl := range []int{24_000, 60_000, 107_000} {
		if _, err := s.Apply(event.AgentEvent{ContextUsed: lvl}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
	su, err := LoadSessionUsage(l, "s1")
	if err != nil {
		t.Fatal(err)
	}
	p := su.Providers["omp/yoga"]
	if p == nil || len(p.Series) != 1 || p.Series[0].ContextUsed != 107_000 || p.ContextUsed != 107_000 {
		t.Fatalf("stopped turn not plotted at its last level: %+v", p)
	}
	if p.Turns != 0 || p.UsageTotals != (UsageTotals{}) {
		t.Fatalf("a level-only point counted a turn or flows: %+v", p)
	}
	// The next turn starts clean: a Done without levels plots nothing.
	if _, err := s.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
	su, _ = LoadSessionUsage(l, "s1")
	if n := len(su.Providers["omp/yoga"].Series); n != 1 {
		t.Fatalf("series = %d points, want 1", n)
	}
}

// An errored turn plots the level it reached and leaves nothing behind;
// a compaction already plots its level, so the usage-less Done after it
// adds no second point; a level point advances LastAt like other writers.
func TestTurnLevelEndsWithErrorAndCompaction(t *testing.T) {
	l := usageLayout(t)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := &Store{layout: l, sessionID: "s1", provider: "omp/yoga", now: func() time.Time { return at }}
	series := func() []UsagePoint {
		su, err := LoadSessionUsage(l, "s1")
		if err != nil {
			t.Fatal(err)
		}
		return su.Providers["omp/yoga"].Series
	}
	if _, err := s.Apply(event.AgentEvent{ContextUsed: 90_000}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "boom"}); err != nil {
		t.Fatal(err)
	}
	if got := series(); len(got) != 1 || got[0].ContextUsed != 90_000 {
		t.Fatalf("errored turn: %+v", got)
	}
	if su, _ := LoadSessionUsage(l, "s1"); !su.Providers["omp/yoga"].LastAt.Equal(at) || !su.UpdatedAt.Equal(at) {
		t.Fatalf("level point left LastAt/UpdatedAt behind: %+v", su)
	}
	if _, err := s.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
	if got := series(); len(got) != 1 {
		t.Fatalf("errored turn's level leaked into the next: %+v", got)
	}

	ci := &event.CompactionInfo{PreTokens: 90_000, PostTokens: 20_000}
	if _, err := s.Apply(event.AgentEvent{Type: event.Compaction, Compaction: ci, ContextUsed: 20_000}); err != nil {
		t.Fatal(err)
	}
	n := len(series())
	if _, err := s.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
	if got := series(); len(got) != n {
		t.Fatalf("compaction level plotted twice: %+v", got)
	}
}
