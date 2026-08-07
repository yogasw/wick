package slack

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A relayed sub-agent event must read as "who is doing what", so an operator
// watching a delegated run can tell the child's work from the leader's.
func TestSubAgentStatusLabelShowsChildAndActivity(t *testing.T) {
	got := subAgentStatusLabel(event.AgentEvent{
		Type:      event.ToolUse,
		SubAgent:  "researcher",
		ToolName:  "Read",
		ToolInput: `{"file_path":"/repo/internal/store.go"}`,
	})
	if want := "researcher → Reading store.go"; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

func TestSubAgentStatusLabelThinking(t *testing.T) {
	got := subAgentStatusLabel(event.AgentEvent{Type: event.Thinking, SubAgent: "coder"})
	if want := "coder → " + statusLabelThinking; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

// A child's finished tool step means the CHILD is deciding its next move; the
// leader is still blocked. So the fallback is the child's working state, still
// attributed to it.
func TestSubAgentStatusLabelToolResultStaysAttributed(t *testing.T) {
	got := subAgentStatusLabel(event.AgentEvent{Type: event.ToolResult, SubAgent: "researcher"})
	if want := "researcher → " + statusLabelWorking; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

// The relay branch must not disturb the leader's own turn bookkeeping: a
// relayed event for a session with no turn is a no-op, not a panic.
func TestOnAgentEventRelayWithNoTurnIsNoop(t *testing.T) {
	c := &Channel{turns: map[string]*turn{}}
	c.OnAgentEvent("slack-123", event.AgentEvent{
		Type: event.ToolUse, SubAgent: "researcher", ToolName: "Read",
	})
	if len(c.turns) != 0 {
		t.Fatalf("relay must not create turn state, got %d", len(c.turns))
	}
}

// HasLiveTurn is what the relay uses to choose the thread to paint. Only a turn
// still in flight counts — see TestHasLiveTurnFalseAfterDone for the finished
// case, which keeps its entry but must not report live.
func TestHasLiveTurnReflectsInFlightTurn(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-123": {running: true}}}
	if !c.HasLiveTurn("slack-123") {
		t.Fatal("session with a turn should report live")
	}
	if c.HasLiveTurn("slack-999") {
		t.Fatal("session without a turn must not report live")
	}
}
