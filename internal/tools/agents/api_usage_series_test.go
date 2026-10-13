package agents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// turnPt is a turn point as recordUsage writes it; levelPt a level-only
// one as recordLevelPoint and recordCompaction write it.
func turnPt(at time.Time, level, input int, cost float64) store.UsagePoint {
	return store.UsagePoint{At: at, ContextUsed: level, Model: "opus", Input: input, CostUSD: cost}
}

func levelPt(at time.Time, level int) store.UsagePoint {
	return store.UsagePoint{At: at, ContextUsed: level}
}

func intsOf(ps []*int) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		if p == nil {
			out[i] = -1
			continue
		}
		out[i] = *p
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A level-only point gets no turn number, no spend, and does not shift
// the turns around it.
func TestSessionContextTrendTurnSkipsLevelOnly(t *testing.T) {
	now := time.Now()
	p := &store.ProviderUsage{Turns: 3, Series: []store.UsagePoint{
		turnPt(now, 100, 10, 0.1),
		levelPt(now.Add(time.Minute), 120),
		turnPt(now.Add(2*time.Minute), 150, 20, 0.2),
		levelPt(now.Add(3*time.Minute), 60), // compaction
		turnPt(now.Add(4*time.Minute), 80, 30, 0.3),
	}}
	p.Input = 60
	su := &store.SessionUsage{Turns: 3, Providers: map[string]*store.ProviderUsage{"claude/default": p}}

	out := sessionContextOf("s1", su, contextTrendMax)
	if got := intsOf(out.TrendTurn); !sameInts(got, []int{1, -1, 2, -1, 3}) {
		t.Fatalf("trend_turn = %v", got)
	}
	if got := intsOf(out.TrendSent); !sameInts(got, []int{10, -1, 20, -1, 30}) {
		t.Fatalf("trend_sent = %v", got)
	}
	if out.TrendCost[1] != nil || out.TrendCost[4] == nil || !costNearAPI(*out.TrendCost[4], 0.3) {
		t.Fatalf("trend_cost wrong: %v", out.TrendCost)
	}
	if out.TrendTotal != 5 || len(out.Trend) != 5 || len(out.TrendAt) != 5 || len(out.TrendSpent) != 5 {
		t.Fatalf("lengths: total=%d trend=%d at=%d spent=%d", out.TrendTotal, len(out.Trend), len(out.TrendAt), len(out.TrendSpent))
	}

	// null, not a missing entry, so the arrays stay aligned.
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"trend_turn":[1,null,2,null,3]`) {
		t.Fatalf("json: %s", raw)
	}
}

// A capped trail keeps the real turn numbers: the provider ran 900
// turns, only the last 500 points survive, and the default view returns
// the last 40 of those.
func TestSessionContextTrendTurnCapped(t *testing.T) {
	now := time.Now()
	series := make([]store.UsagePoint, 0, store.UsageSeriesMax)
	for i := 0; i < store.UsageSeriesMax; i++ {
		series = append(series, turnPt(now.Add(time.Duration(i)*time.Minute), 1000+i, 5, 0.01))
	}
	p := &store.ProviderUsage{Turns: 900, Series: series}
	su := &store.SessionUsage{Turns: 900, Providers: map[string]*store.ProviderUsage{"claude/default": p}}

	def := sessionContextOf("s1", su, contextTrendMax)
	if len(def.Trend) != contextTrendMax || len(def.TrendTurn) != contextTrendMax {
		t.Fatalf("default: want %d points, got %d / %d", contextTrendMax, len(def.Trend), len(def.TrendTurn))
	}
	if *def.TrendTurn[0] != 861 || *def.TrendTurn[contextTrendMax-1] != 900 {
		t.Fatalf("default turns %d..%d, want 861..900", *def.TrendTurn[0], *def.TrendTurn[contextTrendMax-1])
	}
	if def.TrendTotal != store.UsageSeriesMax {
		t.Fatalf("trend_total = %d, want %d", def.TrendTotal, store.UsageSeriesMax)
	}

	all := sessionContextOf("s1", su, 0)
	if len(all.Trend) != store.UsageSeriesMax || len(all.TrendSpent) != store.UsageSeriesMax {
		t.Fatalf("all: want every stored point, got %d", len(all.Trend))
	}
	if *all.TrendTurn[0] != 401 || *all.TrendTurn[store.UsageSeriesMax-1] != 900 {
		t.Fatalf("all turns %d..%d, want 401..900", *all.TrendTurn[0], *all.TrendTurn[store.UsageSeriesMax-1])
	}
	if all.TrendTotal != def.TrendTotal {
		t.Fatalf("trend_total differs between views: %d vs %d", all.TrendTotal, def.TrendTotal)
	}
}

// Every provider carries its own trail; the top level is the active
// one's, under the same keys it always had.
func TestSessionContextSeriesPerProvider(t *testing.T) {
	now := time.Now()
	a := &store.ProviderUsage{Turns: 2, LastAt: now.Add(-time.Hour), Series: []store.UsagePoint{
		turnPt(now.Add(-2*time.Hour), 100, 10, 0.1),
		turnPt(now.Add(-time.Hour), 200, 20, 0.2),
	}}
	b := &store.ProviderUsage{Turns: 1, LastAt: now, Series: []store.UsagePoint{
		turnPt(now, 50, 7, 0.05),
	}}
	su := &store.SessionUsage{Turns: 3, Providers: map[string]*store.ProviderUsage{"claude/a": a, "codex/b": b}}

	out := sessionContextOf("s1", su, contextTrendMax)
	if out.Provider != "codex/b" || len(out.Providers) != 2 || out.Providers[0].Provider != "codex/b" {
		t.Fatalf("active/order wrong: %q %+v", out.Provider, out.Providers)
	}
	if !sameInts(out.Trend, []int{50}) || !sameInts(intsOf(out.TrendTurn), []int{1}) {
		t.Fatalf("top level is not the active trail: %v %v", out.Trend, intsOf(out.TrendTurn))
	}
	other := out.Providers[1]
	if other.Provider != "claude/a" || !sameInts(other.Trend, []int{100, 200}) ||
		!sameInts(intsOf(other.TrendTurn), []int{1, 2}) || !sameInts(intsOf(other.TrendSent), []int{10, 20}) ||
		len(other.TrendAt) != 2 || other.TrendTotal != 2 {
		t.Fatalf("other provider trail wrong: %+v", other.ContextSeriesDTO)
	}

	raw, _ := json.Marshal(out)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"trend", "trend_at", "trend_spent", "trend_turn", "trend_sent", "trend_cost", "trend_total"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("top-level %q missing: %s", k, raw)
		}
	}
	rows := m["providers"].([]any)
	if _, ok := rows[1].(map[string]any)["trend_turn"]; !ok {
		t.Fatalf("provider row has no trail: %s", raw)
	}
}

func costNearAPI(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }
