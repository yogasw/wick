package slack

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A Stop hook that refuses the text a turn ended on sends the model back
// to work: only the text after the hook is the reply.
func TestReplyTextDropsTextRefusedByStopHook(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-1": {running: true}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "Waiting for the teammate…"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextReplace})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "final answer"})

	c.mu.Lock()
	got := c.turns["slack-1"].replyText()
	c.mu.Unlock()
	if got != "final answer" {
		t.Fatalf("replyText = %q, want only the text after the hook", got)
	}
}

// Refused text with nothing after it posts nothing.
func TestReplyTextEmptyAfterStopHookWithoutText(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-1": {running: true}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "interim line"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextReplace})

	c.mu.Lock()
	got := c.turns["slack-1"].replyText()
	c.mu.Unlock()
	if got != "" {
		t.Fatalf("replyText = %q, want empty", got)
	}
}
