package event

import (
	"math"
	"testing"
)

// Claude keeps one process alive for the whole session, and its result
// frame's modelUsage / total_cost_usd are running totals of that
// process. These tests pin that each turn is charged only its own share.

func parseDone(t *testing.T, p *ClaudeParser, line string) AgentEvent {
	t.Helper()
	ev, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return ev
}

func costNear(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestClaudeTurnSpendIsDifference: the shape of the real ledger — two
// turns 5 s apart whose totals differ by +461 output tokens and $0.04.
// The second turn must carry that difference, not the running total.
func TestClaudeTurnSpendIsDifference(t *testing.T) {
	p := NewClaudeParser()
	first := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":6.33,`+
		`"usage":{"input_tokens":2,"cache_read_input_tokens":90000,"output_tokens":100},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":5000,"outputTokens":30000,"cacheReadInputTokens":20290000,"cacheCreationInputTokens":400000,"contextWindow":1000000}}}`)
	if first.Usage == nil {
		t.Fatal("first: want usage")
	}
	// The first result of a process is taken whole.
	if first.Usage.CacheRead != 20290000 || first.Usage.Output != 30000 || !costNear(first.Usage.CostUSD, 6.33) {
		t.Fatalf("first: want the full totals, got %+v", first.Usage)
	}

	second := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":6.37,`+
		`"usage":{"input_tokens":3,"cache_read_input_tokens":91000,"output_tokens":461},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":5003,"outputTokens":30461,"cacheReadInputTokens":20410000,"cacheCreationInputTokens":400000,"contextWindow":1000000}}}`)
	u := second.Usage
	if u == nil {
		t.Fatal("second: want usage")
	}
	if u.Input != 3 || u.Output != 461 || u.CacheRead != 120000 || u.CacheWrite != 0 {
		t.Fatalf("second: want only this turn's tokens, got %+v", u)
	}
	if !costNear(u.CostUSD, 0.04) {
		t.Fatalf("second: want cost 0.04, got %v", u.CostUSD)
	}
	if u.Model != "claude-opus-5" || u.Window != 1000000 {
		t.Fatalf("second: model/window lost: %q / %d", u.Model, u.Window)
	}
}

// TestClaudeTurnSpendCounterDrop: a counter going DOWN means the totals
// were reset under us; the new value is the turn's spend as it stands.
func TestClaudeTurnSpendCounterDrop(t *testing.T) {
	p := NewClaudeParser()
	parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":5,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":900,"outputTokens":900,"cacheReadInputTokens":900000}}}`)
	ev := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.2,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":50,"outputTokens":1000,"cacheReadInputTokens":3000}}}`)
	u := ev.Usage
	if u == nil || u.Input != 50 || u.Output != 1000 || u.CacheRead != 3000 || !costNear(u.CostUSD, 0.2) {
		t.Fatalf("want the new values as-is, got %+v", u)
	}
	// And the dropped values are the new baseline.
	ev = parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.5,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":60,"outputTokens":1100,"cacheReadInputTokens":5000}}}`)
	u = ev.Usage
	if u == nil || u.Input != 10 || u.Output != 100 || u.CacheRead != 2000 || !costNear(u.CostUSD, 0.3) {
		t.Fatalf("after drop: want the difference from the new baseline, got %+v", u)
	}
}

// TestClaudeTurnSpendTwoModels: the baseline is kept per model name. A
// turn that only used the main model must not be charged for the side
// model's earlier work, and the reported model is the one this turn used.
func TestClaudeTurnSpendTwoModels(t *testing.T) {
	p := NewClaudeParser()
	parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":1,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-haiku-4-5":{"inputTokens":800,"outputTokens":50,"contextWindow":200000},`+
		`"claude-opus-5":{"inputTokens":100,"outputTokens":200,"cacheReadInputTokens":500,"contextWindow":1000000}}}`)
	ev := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":1.5,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-haiku-4-5":{"inputTokens":800,"outputTokens":50,"contextWindow":200000},`+
		`"claude-opus-5":{"inputTokens":110,"outputTokens":260,"cacheReadInputTokens":1500,"contextWindow":1000000}}}`)
	u := ev.Usage
	if u == nil {
		t.Fatal("want usage")
	}
	if u.Model != "claude-opus-5" || u.Window != 1000000 {
		t.Fatalf("want opus (the model this turn used), got %q / %d", u.Model, u.Window)
	}
	if u.Input != 10 || u.Output != 60 || u.CacheRead != 1000 || !costNear(u.CostUSD, 0.5) {
		t.Fatalf("want opus's difference only, got %+v", u)
	}
}

// TestClaudeTurnSpendErrorMovesBaseline: an error result carries the
// process totals too (it reports what was spent before stopping). It
// must move the baseline, or the next turn pays for it a second time.
func TestClaudeTurnSpendErrorMovesBaseline(t *testing.T) {
	p := NewClaudeParser()
	parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":1,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":100,"outputTokens":100,"cacheReadInputTokens":1000}}}`)
	errEv := parseDone(t, p, `{"type":"result","subtype":"error_max_turns","is_error":true,"total_cost_usd":3,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":300,"outputTokens":300,"cacheReadInputTokens":5000}}}`)
	if errEv.Type != Error {
		t.Fatalf("want Error, got %v", errEv.Type)
	}
	ev := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":3.25,`+
		`"usage":{"input_tokens":1,"output_tokens":1},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":310,"outputTokens":320,"cacheReadInputTokens":6000}}}`)
	u := ev.Usage
	if u == nil || u.Input != 10 || u.Output != 20 || u.CacheRead != 1000 || !costNear(u.CostUSD, 0.25) {
		t.Fatalf("want only the turn after the error, got %+v", u)
	}
}

// TestClaudeTurnSpendLevelUnchanged: differencing the flows must not
// touch the context level, which comes from the last request.
func TestClaudeTurnSpendLevelUnchanged(t *testing.T) {
	p := NewClaudeParser()
	parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":1,`+
		`"usage":{"input_tokens":5,"cache_creation_input_tokens":1000,"cache_read_input_tokens":40000,"output_tokens":10},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":500,"outputTokens":100,"cacheReadInputTokens":400000,"contextWindow":1000000}}}`)
	// An assistant frame reports this turn's real window.
	parseDone(t, p, `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],`+
		`"usage":{"input_tokens":3,"cache_creation_input_tokens":200,"cache_read_input_tokens":41000,"output_tokens":5}}}`)
	ev := parseDone(t, p, `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":1.1,`+
		`"usage":{"input_tokens":3,"cache_creation_input_tokens":200,"cache_read_input_tokens":41000,"output_tokens":5},`+
		`"modelUsage":{"claude-opus-5":{"inputTokens":503,"outputTokens":105,"cacheReadInputTokens":441000,"contextWindow":1000000}}}`)
	if ev.Usage == nil || ev.Usage.ContextUsed != 41203 {
		t.Fatalf("level: want 41203, got %+v", ev.Usage)
	}
	if ev.Usage.CacheRead != 41000 {
		t.Fatalf("flow: want 41000 cache read, got %d", ev.Usage.CacheRead)
	}
}

// TestClaudeTurnSpendTurnScoped: the wick engine writes per-turn numbers
// in claude's shape and says so; those are taken as they stand.
func TestClaudeTurnSpendTurnScoped(t *testing.T) {
	p := NewClaudeParser()
	line := `{"type":"result","subtype":"success","is_error":false,"usage_scope":"turn",` +
		`"usage":{"input_tokens":10,"cache_read_input_tokens":0,"output_tokens":5},` +
		`"modelUsage":{"gemini-2.5":{"inputTokens":10,"outputTokens":5}}}`
	parseDone(t, p, line)
	ev := parseDone(t, p, line)
	if ev.Usage == nil || ev.Usage.Input != 10 || ev.Usage.Output != 5 {
		t.Fatalf("want the turn's own numbers, got %+v", ev.Usage)
	}
}
