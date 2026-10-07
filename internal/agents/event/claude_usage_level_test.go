package event

import "testing"

// A turn that loops through tool calls makes several requests. The
// result frame sums them all, so reading its .usage as the context
// level reports several times what the window actually held — the
// meter showed 337% of a 1M window on a real session. The level must
// come from the LAST request instead.
func TestClaudeContextLevelIsLastRequestNotTurnSum(t *testing.T) {
	p := NewClaudeParser()
	lines := []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":31173,"cache_read_input_tokens":0,"output_tokens":3},"content":[{"type":"text","text":"a"}]}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":8,"cache_creation_input_tokens":3199,"cache_read_input_tokens":31173,"output_tokens":1},"content":[{"type":"text","text":"b"}]}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":8,"cache_creation_input_tokens":146,"cache_read_input_tokens":34519,"output_tokens":1},"content":[{"type":"text","text":"c"}]}}`,
		`{"type":"result","subtype":"success","result":"done","total_cost_usd":0.08,` +
			`"usage":{"input_tokens":34,"cache_creation_input_tokens":34665,"cache_read_input_tokens":100064,"output_tokens":450},` +
			`"modelUsage":{"claude-haiku-4-5":{"inputTokens":1074,"outputTokens":461,"cacheReadInputTokens":100064,"cacheCreationInputTokens":34665,"contextWindow":200000}}}`,
	}
	var done AgentEvent
	for _, l := range lines {
		ev, err := p.Parse(l)
		if err != nil {
			t.Fatalf("parse %q: %v", l, err)
		}
		if ev.Type == Done {
			done = ev
		}
	}
	if done.Usage == nil {
		t.Fatal("result frame produced no usage")
	}
	// 8 + 146 + 34519 — the window as the last request saw it.
	if got, want := done.Usage.ContextUsed, 34673; got != want {
		t.Fatalf("ContextUsed = %d, want %d (turn sum would be 134763)", got, want)
	}
	// Flows still come from modelUsage: the turn really did pay for
	// every cached re-read.
	if got, want := done.Usage.CacheRead, 100064; got != want {
		t.Fatalf("CacheRead = %d, want %d", got, want)
	}
	if got, want := done.Usage.Window, 200000; got != want {
		t.Fatalf("Window = %d, want %d", got, want)
	}
}

// Some providers emit claude-shaped lines without per-message usage but
// with the result frame's .iterations list. The last iteration is the
// same last request, so the level still comes out right.
func TestClaudeContextLevelFallsBackToIterations(t *testing.T) {
	p := NewClaudeParser()
	line := `{"type":"result","subtype":"success","usage":{"input_tokens":34,"cache_creation_input_tokens":34665,"cache_read_input_tokens":100064,"output_tokens":450,` +
		`"iterations":[{"input_tokens":10,"cache_creation_input_tokens":31173,"cache_read_input_tokens":0},{"input_tokens":8,"cache_creation_input_tokens":146,"cache_read_input_tokens":34519}]}}`
	ev, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Usage == nil || ev.Usage.ContextUsed != 34673 {
		t.Fatalf("ContextUsed = %v, want 34673", ev.Usage)
	}
}

// The level belongs to the turn that measured it: a turn whose frames
// report nothing must not inherit the previous turn's reading.
func TestClaudeContextLevelDoesNotLeakAcrossTurns(t *testing.T) {
	p := NewClaudeParser()
	for _, l := range []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":5,"cache_read_input_tokens":1000,"output_tokens":1},"content":[{"type":"text","text":"a"}]}}`,
		`{"type":"result","subtype":"success","usage":{"input_tokens":5,"cache_read_input_tokens":1000,"output_tokens":1}}`,
	} {
		if _, err := p.Parse(l); err != nil {
			t.Fatalf("parse: %v", err)
		}
	}
	ev, err := p.Parse(`{"type":"result","subtype":"success","usage":{"input_tokens":7,"output_tokens":2}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Usage == nil || ev.Usage.ContextUsed != 7 {
		t.Fatalf("ContextUsed = %v, want 7 (this turn's own reading)", ev.Usage)
	}
}
