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
