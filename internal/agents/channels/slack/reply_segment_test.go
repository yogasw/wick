package slack

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A turn that answers, runs a few tools, and closes on a lone [silent]
// line must still post its answer: the marker counts only at the start.
func TestReplyTextSkipsTrailingSilentSegment(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-1": {running: true}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "Here is the answer.\n```\ncode\n```"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "todo"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolResult, ToolName: "todo"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "\n\n[silent]"})

	c.mu.Lock()
	got := c.turns["slack-1"].replyText()
	c.mu.Unlock()
	if got != "Here is the answer.\n```\ncode\n```" {
		t.Fatalf("replyText = %q, want the answer before the marker", got)
	}
}

func TestReplyTextKeepsLastRealSegment(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-1": {running: true}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "checking"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "todo"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "final answer"})

	c.mu.Lock()
	got := c.turns["slack-1"].replyText()
	c.mu.Unlock()
	if got != "final answer" {
		t.Fatalf("replyText = %q, want the last segment", got)
	}
}

func TestBlankSegment(t *testing.T) {
	for in, want := range map[string]bool{
		"":                    true,
		"  \n":                true,
		"\n\n[silent]":        true,
		"[SILENT]  \n":        true,
		"[silent] note":       false,
		"text\n[silent]":      false,
		"talk about [silent]": false,
	} {
		if got := blankSegment(in); got != want {
			t.Errorf("blankSegment(%q) = %v, want %v", in, got, want)
		}
	}
}
