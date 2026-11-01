package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
)

// A Team task whose agent was idle-killed and is being retried keeps
// collecting: the Done of the exit is not the turn's answer, the
// restarted process's turn is.
func TestCollectTurnWaitsThroughRestartingExit(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 4)
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "half"}
	ch <- delegation.StreamEvent{Type: event.Done, Raw: delegation.ExitDoneRaw(true)}
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "the answer"}
	ch <- delegation.StreamEvent{Type: event.Done}
	if got := collectTurn(context.Background(), ch); got != "the answer" {
		t.Fatalf("collected %q, want the restarted turn's answer", got)
	}
}

// An exit that is not restarted still ends the turn.
func TestCollectTurnEndsOnFinalExit(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 2)
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "half"}
	ch <- delegation.StreamEvent{Type: event.Done, Raw: delegation.ExitDoneRaw(false)}
	if got := collectTurn(context.Background(), ch); got != "half" {
		t.Fatalf("collected %q", got)
	}
}

// Narration between tool calls is not the answer: only the text after
// the last tool call is.
func TestCollectTurnKeepsTextAfterLastTool(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 8)
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "looking it up"}
	ch <- delegation.StreamEvent{Type: event.ToolUse}
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "adjusting"}
	ch <- delegation.StreamEvent{Type: event.ToolUse}
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "the "}
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "answer"}
	ch <- delegation.StreamEvent{Type: event.Done}
	if got := collectTurn(context.Background(), ch); got != "the answer" {
		t.Fatalf("collected %q, want only the final answer", got)
	}
}

// A tool call closing the turn with no text after it keeps the answer
// written before it.
func TestCollectTurnKeepsAnswerBeforeTrailingTool(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 4)
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "the answer"}
	ch <- delegation.StreamEvent{Type: event.ToolUse}
	ch <- delegation.StreamEvent{Type: event.Done}
	if got := collectTurn(context.Background(), ch); got != "the answer" {
		t.Fatalf("collected %q", got)
	}
}
