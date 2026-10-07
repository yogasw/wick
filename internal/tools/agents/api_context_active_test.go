package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// The ordinary case: whichever provider answered last owns the meter.
func TestActiveContextProviderPrefersTheNewestReading(t *testing.T) {
	now := time.Now()
	ps := map[string]*store.ProviderUsage{
		"claude/default": {LastAt: now.Add(-time.Hour)},
		"codex/default":  {LastAt: now},
	}
	if got := activeContextProvider(ps); got != "codex/default" {
		t.Fatalf("active = %q, want codex/default", got)
	}
}

// A run still on its FIRST turn has a level but no LastAt — the ledger
// only stamps that when a turn completes. Skipping it blanked the whole
// meter for exactly the sub-agents people open the panel to watch.
func TestActiveContextProviderCountsAnUnfinishedFirstTurn(t *testing.T) {
	ps := map[string]*store.ProviderUsage{
		"claude/enginer": {ContextUsed: 197787},
	}
	if got := activeContextProvider(ps); got != "claude/enginer" {
		t.Fatalf("active = %q, want the provider that already reported a level", got)
	}
}

// Map iteration is random, so a tie has to break on something stable:
// a meter that names a different provider on each poll is worse than one
// that names an arbitrary but unchanging one.
func TestActiveContextProviderBreaksTiesStably(t *testing.T) {
	ps := map[string]*store.ProviderUsage{
		"zeta/default":   {},
		"alpha/default":  {},
		"middle/default": {},
	}
	for i := 0; i < 20; i++ {
		if got := activeContextProvider(ps); got != "alpha/default" {
			t.Fatalf("active = %q on run %d, want alpha/default every time", got, i)
		}
	}
}

// A finished turn still outranks a bare level, whatever the names are.
func TestActiveContextProviderFinishedTurnBeatsBareLevel(t *testing.T) {
	ps := map[string]*store.ProviderUsage{
		"aaa/default": {ContextUsed: 500_000},
		"zzz/default": {LastAt: time.Now(), ContextUsed: 10},
	}
	if got := activeContextProvider(ps); got != "zzz/default" {
		t.Fatalf("active = %q, want the provider that actually finished a turn", got)
	}
}

func TestActiveContextProviderEmptyLedger(t *testing.T) {
	if got := activeContextProvider(nil); got != "" {
		t.Fatalf("active = %q, want empty for a session that has not run", got)
	}
}
