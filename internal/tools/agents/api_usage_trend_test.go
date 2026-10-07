package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// The cumulative spend must be anchored to the exact total and walked
// BACKWARDS. A forward sum over the trail would start from whatever
// survived the cap and under-report every point by the history that
// fell off the end — silently, and worst on exactly the long sessions
// people open this panel for.
func TestTrendSpentAnchorsToTheExactTotal(t *testing.T) {
	now := time.Now()
	series := make([]store.UsagePoint, 0, contextTrendMax+10)
	for i := 0; i < contextTrendMax+10; i++ {
		series = append(series, store.UsagePoint{At: now.Add(time.Duration(i) * time.Minute), Input: 100})
	}
	// The provider's real total includes history the trail no longer has.
	total := 1_000_000

	spent := trendSpentOf(series, total)
	if len(spent) != contextTrendMax {
		t.Fatalf("len = %d, want the same window trendOf returns (%d)", len(spent), contextTrendMax)
	}
	if spent[len(spent)-1] != total {
		t.Fatalf("last point = %d, want the exact total %d", spent[len(spent)-1], total)
	}
	// Each step back is one turn's spend.
	if d := spent[len(spent)-1] - spent[len(spent)-2]; d != 100 {
		t.Fatalf("step = %d, want one turn's 100 tokens", d)
	}
	// And the oldest point still carries the invisible history, rather
	// than starting from zero.
	if spent[0] <= 0 || spent[0] >= total {
		t.Fatalf("oldest = %d, want it to sit between the lost history and the total", spent[0])
	}
}

// A compaction point records a level and no flows; it must not look like
// a turn that cost something.
func TestTrendSpentIgnoresLevelOnlyPoints(t *testing.T) {
	now := time.Now()
	series := []store.UsagePoint{
		{At: now.Add(-2 * time.Minute), Input: 50, Output: 10},
		{At: now.Add(-time.Minute), ContextUsed: 5000}, // compaction
		{At: now, Input: 40},
	}
	spent := trendSpentOf(series, 100)
	if spent[1] != spent[0] {
		t.Fatalf("compaction changed the running total: %v", spent)
	}
	if spent[2] != 100 {
		t.Fatalf("newest = %d, want the exact total", spent[2])
	}
}
