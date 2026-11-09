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

// playThreadTurn runs: answer text, a tool call with input, narration,
// one more tool, then the closing text; it returns the turn's reply.
func playThreadTurn(t *testing.T, input, closing string) string {
	t.Helper()
	c := &Channel{turns: map[string]*turn{"slack-1": {channelID: "C01TESTCHAN", threadTS: "1700000000.000100", running: true}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "Posting the answer."})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "mcp__wick__wick_execute", ToolInput: input, ToolUseID: "a"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "a", Text: "ok"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "Good. Now the log and the archive."})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{"command":"ls"}`, ToolUseID: "b"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "b", Text: "ok"})
	if closing != "" {
		c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: closing})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turns["slack-1"].replyText()
}

// A turn that posted its answer into its own thread through a tool and
// then closed on a tool (or a bare [silent]) posts nothing: the text
// before its last tool is narration, not an answer.
func TestReplyTextAfterToolPostToThreadIsEmpty(t *testing.T) {
	send := `{"tool_id":"conn:x/send_message","params":{"channel":"C01TESTCHAN","thread_ts":"1700000000.000100","text":"answer"}}`
	for _, closing := range []string{"", "[silent]"} {
		if got := playThreadTurn(t, send, closing); got != "" {
			t.Fatalf("closing %q: replyText = %q, want empty", closing, got)
		}
	}
	// Real closing text still goes out.
	if got := playThreadTurn(t, send, "Done, see above."); got != "Done, see above." {
		t.Fatalf("replyText = %q", got)
	}
}

// Reading the thread, or posting to another thread, keeps the fallback.
func TestReplyTextFallbackWithoutThreadPost(t *testing.T) {
	for _, input := range []string{
		`{"tool_id":"conn:x/get_thread_replies","params":{"channel":"C01TESTCHAN","thread_ts":"1700000000.000100"}}`,
		`{"tool_id":"conn:x/send_message","params":{"channel":"C01OTHER","thread_ts":"1700000000.000999","text":"note"}}`,
	} {
		if got := playThreadTurn(t, input, ""); got != "Good. Now the log and the archive." {
			t.Fatalf("input %s: replyText = %q", input, got)
		}
	}
}

// A turn nobody typed in the thread (a teammate's task waking the agent)
// that closes on a lone [silent] posts nothing: the line it wrote before
// its last tools is narration, not an answer.
func TestReplyTextUntypedTurnHasNoFallback(t *testing.T) {
	c := &Channel{turns: map[string]*turn{"slack-1": {channelID: "C01TESTCHAN", threadTS: "1.1"}}}
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "Now the docs."})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "Edit"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolResult, ToolName: "Edit"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "\n\n[silent]"})

	c.mu.Lock()
	tr := c.turns["slack-1"]
	untyped, got := tr.untyped, tr.replyText()
	c.mu.Unlock()
	if !untyped {
		t.Fatal("reopened turn not marked untyped")
	}
	if got != "" {
		t.Fatalf("replyText = %q, want nothing", got)
	}

	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash"})
	c.OnAgentEvent("slack-1", event.AgentEvent{Type: event.TextDelta, Text: "final answer"})
	c.mu.Lock()
	got = c.turns["slack-1"].replyText()
	c.mu.Unlock()
	if got != "final answer" {
		t.Fatalf("replyText = %q, want the closing text", got)
	}
}
