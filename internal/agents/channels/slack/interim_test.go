package slack

import (
	"strings"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
)

// interimHooks wires the switch and the open tasks for one test.
func interimHooks(t *testing.T, hide bool, open map[string]bool) {
	t.Helper()
	prevHide, prevActive := agentchannels.HideInterim, agentchannels.TasksActive
	t.Cleanup(func() { agentchannels.HideInterim, agentchannels.TasksActive = prevHide, prevActive })
	agentchannels.HideInterim = func(string) bool { return hide }
	agentchannels.TasksActive = func(ids []string) bool {
		for _, id := range ids {
			if open[id] {
				return true
			}
		}
		return false
	}
}

const interimLine = "Sudah saya tanyakan ke Franky."

// playInterim sends team_message (tool), its result, text, then Done,
// and returns what the thread got.
func playInterim(t *testing.T, tool, text string) string {
	t.Helper()
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: tool, ToolInput: `{"to":"@franky","message":"cek"}`, ToolUseID: "u1"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolResult, ToolUseID: "u1", Text: `{"task_id":"t1","state":"working"}`})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: text})
	// A live edit tick while the turn holds posts nothing.
	c.flushLiveMessage("slack-t1")
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
	return postedText(f)
}

func TestInterimHeldWhileTaskWorks(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	if got := playInterim(t, "mcp__wick__team_message", interimLine); got != "" {
		t.Fatalf("in-between line posted: %q", got)
	}
}

func TestInterimPostsWhenTasksSettled(t *testing.T) {
	interimHooks(t, true, map[string]bool{})
	if got := playInterim(t, "team_message", interimLine); !strings.Contains(got, interimLine) {
		t.Fatalf("posted %q", got)
	}
}

func TestInterimGuard1Posts(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	q := "Sudah saya tanyakan. Mau sekalian dicek prod?"
	if got := playInterim(t, "team_message", q); !strings.Contains(got, q) {
		t.Fatalf("posted %q", got)
	}
}

func TestInterimSwitchOffPosts(t *testing.T) {
	interimHooks(t, false, map[string]bool{"t1": true})
	if got := playInterim(t, "team_message", interimLine); !strings.Contains(got, interimLine) {
		t.Fatalf("posted %q", got)
	}
}

// A turn that did not call team_message streams as before: the first
// live edit posts its text, before the Done.
func TestInterimNoTeamMessageNotHeld(t *testing.T) {
	interimHooks(t, true, map[string]bool{"t1": true})
	f := newFakeSlack(t)
	c := &Channel{
		api:   f.client(),
		turns: map[string]*turn{"slack-t1": {channelID: "C1", threadTS: "1700000000.000100", running: true}},
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{}`, ToolUseID: "u0"})
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: interimLine})
	c.flushLiveMessage("slack-t1")
	if got := postedText(f); !strings.Contains(got, interimLine) {
		t.Fatalf("live edit held a turn without team_message: %q", got)
	}
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.Done})
}
