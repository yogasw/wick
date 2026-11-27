package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
)

// A Team agent's chats open in the Team app: the main chat on the agent's
// page, any other chat with ?session=. Sessions the Team app does not list
// under an agent (sub-agents, other projects, missing) get "".
func TestTeamSessionPath(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p9", "u1")
	franky := seedTeamAgent(t, "u1", "franky", "p1")
	if _, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{
		ID: "main-chat", ProjectID: "p1", Origin: session.OriginUI, UserID: "u1", AgentID: franky.ID, AgentMain: true,
	}); err != nil {
		t.Fatal(err)
	}
	newSlackSession(t, "slack-thread", "p1", "u1", "")
	newSlackSession(t, "slack-sub", "p1", "u1", "slack-thread")
	newSlackSession(t, "plain", "p9", "u1", "")

	for id, want := range map[string]string{
		"main-chat":    "/tools/agents/team/franky",
		"slack-thread": "/tools/agents/team/franky?session=slack-thread",
		"slack-sub":    "",
		"plain":        "",
		"nope":         "",
		"../x":         "",
	} {
		if got := TeamSessionPath(id); got != want {
			t.Errorf("%s: got %q, want %q", id, got, want)
		}
	}
}
