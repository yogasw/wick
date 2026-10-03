package session

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// A new top-level session in a project an agent owns is stamped with that
// agent; an explicit AgentID and a sub-agent's session are left alone.
func TestCreateStampsProjectAgent(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if _, err := project.Create(layout, project.CreateOptions{ID: "p1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	prev := ProjectAgent
	ProjectAgent = func(pid string) string {
		if pid == "p1" {
			return "agent-1"
		}
		return ""
	}
	t.Cleanup(func() { ProjectAgent = prev })

	for _, tc := range []struct {
		id, agent, parent, want string
	}{
		{"s1", "", "", "agent-1"},
		{"s2", "other", "", "other"},
		{"s3", "", "s1", ""},
	} {
		s, err := Create(context.Background(), layout, CreateOptions{ID: tc.id, ProjectID: "p1", Origin: OriginSlack, AgentID: tc.agent, ParentSessionID: tc.parent})
		if err != nil {
			t.Fatal(err)
		}
		if s.Meta.AgentID != tc.want {
			t.Errorf("%s: AgentID %q, want %q", tc.id, s.Meta.AgentID, tc.want)
		}
	}
}
