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

// A thread opened by an agent's own Slack bot belongs to that agent even in a
// project with no agent tag, at Create and when an older meta is loaded.
func TestAgentSlackSessionIDStampsAgent(t *testing.T) {
	const agent = "3ce555d2-b635-4826-a81d-d6bc0d37b632"
	for id, want := range map[string]string{
		"slackagent-" + agent + "-1792133992.257689": agent,
		"slack-" + agent + "-1792133992.257689":      "",
		"slackagent-short-1792133992.257689":         "",
		"4704ee8c-2829-4137-980d-c914e4965739":       "",
	} {
		if got := AgentOfSessionID(id); got != want {
			t.Errorf("AgentOfSessionID(%q) = %q, want %q", id, got, want)
		}
	}

	layout := config.NewLayout(t.TempDir())
	if _, err := project.Create(layout, project.CreateOptions{ID: "p1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	id := "slackagent-" + agent + "-1.2"
	s, err := Create(context.Background(), layout, CreateOptions{ID: id, ProjectID: "p1", Origin: OriginSlack})
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.AgentID != agent {
		t.Fatalf("Create: AgentID %q, want %q", s.Meta.AgentID, agent)
	}
	s.Meta.AgentID = ""
	if err := SaveMeta(layout, id, s.Meta); err != nil {
		t.Fatal(err)
	}
	got, err := Load(layout, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.AgentID != agent {
		t.Fatalf("Load: AgentID %q, want %q", got.Meta.AgentID, agent)
	}
}
