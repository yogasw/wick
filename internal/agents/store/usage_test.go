package store

import (
	"testing"
	"time"
)

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
