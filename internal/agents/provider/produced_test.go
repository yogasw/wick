package provider

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// TestProducedIgnoresSyntheticText: claude's limit notice arrives as text
// but is not the model's output — a turn that got only that far has
// produced nothing, so it can still be re-run elsewhere.
func TestProducedIgnoresSyntheticText(t *testing.T) {
	cases := []struct {
		name string
		ev   event.AgentEvent
		want bool
	}{
		{"model text", event.AgentEvent{Type: event.TextDelta, Text: "hi"}, true},
		{"synthetic limit text", event.AgentEvent{Type: event.TextDelta, Text: "You've hit your session limit", Synthetic: true, ErrKind: event.ErrLimit}, false},
		{"tool use", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash"}, true},
		{"tool result", event.AgentEvent{Type: event.ToolResult}, true},
		{"thinking", event.AgentEvent{Type: event.Thinking, Text: "hmm"}, false},
		{"error", event.AgentEvent{Type: event.Error, ErrKind: event.ErrLimit}, false},
	}
	for _, c := range cases {
		if got := countsAsProduced(c.ev); got != c.want {
			t.Errorf("%s: countsAsProduced = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestProducedClaudeLimitTurn runs claude's real two-frame limit turn
// through the parser: nothing in it counts as produced.
func TestProducedClaudeLimitTurn(t *testing.T) {
	p := event.NewClaudeParser()
	for _, l := range []string{
		`{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 4:40pm (Asia/Jakarta)"}]}}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"You've hit your session limit · resets 4:40pm (Asia/Jakarta)"}`,
	} {
		ev, err := p.Parse(l)
		if err != nil {
			t.Fatal(err)
		}
		if countsAsProduced(ev) {
			t.Fatalf("%v counted as produced: %+v", ev.Type, ev)
		}
	}
}
