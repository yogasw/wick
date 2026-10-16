package agents

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

type fakeProgress struct{ calls []string }

func (f *fakeProgress) TurnText(s, d string) { f.calls = append(f.calls, "text "+s+" "+d) }
func (f *fakeProgress) TurnTool(s, t string) { f.calls = append(f.calls, "tool "+s+" "+t) }

// A turn's streamed text and tool calls reach the Hub's progress, the
// tool as the roster's short label; nothing else does.
func TestTeamTurnActivity(t *testing.T) {
	b := NewBroadcaster()
	f := &fakeProgress{}
	b.OnTurnActivity(func(sessionID string, ev event.AgentEvent) { teamTurnActivity(f, sessionID, ev) })
	b.Publish("S", "a", event.AgentEvent{Type: event.TextDelta, Text: "Checking Loki…"})
	b.Publish("S", "a", event.AgentEvent{Type: event.ToolUse, ToolName: "mcp__wick__wick_execute", ToolInput: `{"tool_id":"conn:loki/query_range"}`, ToolUseID: "t1"})
	b.Publish("S", "a", event.AgentEvent{Type: event.ToolResult, Text: "secret result", ToolUseID: "t1"})
	b.Publish("S", "a", event.AgentEvent{Type: event.Thinking, Text: "hmm"})
	b.Publish("S", "a", event.AgentEvent{Type: event.Done})
	b.Publish("", "a", event.AgentEvent{Type: event.TextDelta, Text: "no session"})
	want := []string{"text S Checking Loki…", "tool S query_range"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %q, want %q", f.calls, want)
	}
}
