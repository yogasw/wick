package slack

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// playTurn plays a turn of text and tool calls ("" = a tool call), then
// Done, and returns what the thread got.
func playTurn(t *testing.T, parts ...string) string {
	t.Helper()
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	for i, p := range parts {
		if p == "" {
			id := string(rune('a' + i))
			c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{"command":"ls"}`, ToolUseID: id})
			c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: id, Text: "ok"})
			continue
		}
		c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: p})
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	return postedText(f)
}

// The narration between tool calls stays out of the thread: it gets the
// turn's last text.
func TestNarrationBetweenToolsNotPosted(t *testing.T) {
	got := playTurn(t, "Still tracing the logs.", "", "Checking the config next.", "", "The webhook returned 500 at 10:02.")
	if strings.Contains(got, "Still tracing") || strings.Contains(got, "Checking the config") || !strings.Contains(got, "The webhook returned 500") {
		t.Fatalf("posted %q", got)
	}
}

// A turn that ended on a tool call posts the last text it wrote.
func TestTurnEndingOnToolPostsLastText(t *testing.T) {
	got := playTurn(t, "Looking.", "", "The answer is 42.", "")
	if strings.Contains(got, "Looking.") || !strings.Contains(got, "The answer is 42.") {
		t.Fatalf("posted %q", got)
	}
}

// A turn with no tool call posts all of its text.
func TestTurnWithoutToolsPostsAll(t *testing.T) {
	if got := playTurn(t, "one ", "two"); !strings.Contains(got, "one two") {
		t.Fatalf("posted %q", got)
	}
}

// withLiveStream switches the live edit of a turn's text on for a test of
// that path.
func withLiveStream(t *testing.T) {
	t.Helper()
	prev := liveStreamText
	liveStreamText = true
	t.Cleanup(func() { liveStreamText = prev })
}

// playError runs a turn that fails with a limit error in channelID, then
// the Done that may follow, and returns what was posted.
func playError(t *testing.T, channelID string) string {
	t.Helper()
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: channelID, threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "half an answer"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Error, ErrorMsg: "usage limit reached"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	return postedText(f)
}

// A channel thread gets no error text and no text of the failed turn.
func TestErrorNotPostedInChannelThread(t *testing.T) {
	if got := playError(t, "C1"); got != "" {
		t.Fatalf("posted %q", got)
	}
}

// A direct message with the bot still says what went wrong.
func TestErrorPostedInDirectMessage(t *testing.T) {
	got := playError(t, "D1")
	if !strings.Contains(got, "usage limit reached") || strings.Contains(got, "half an answer") {
		t.Fatalf("posted %q", got)
	}
}
