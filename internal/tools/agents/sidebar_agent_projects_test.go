package agents

import (
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
)

func TestWithoutAgentProjects(t *testing.T) {
	projects := map[string]project.Project{
		"plain":   {Meta: project.Meta{ID: "plain"}},
		"captain": {Meta: project.Meta{ID: "captain", Tags: []string{project.AgentTag}}},
		"hunter":  {Meta: project.Meta{ID: "hunter", Tags: []string{project.AgentTag}}},
		// An agent project set to show in Projects too keeps its row.
		"shown": {Meta: project.Meta{ID: "shown", Tags: []string{project.AgentTag, project.VisibleTag}}},
		"mine":  {Meta: project.Meta{ID: "mine", Tags: []string{project.PersonalTag}}},
		// A user's own "agent" label is not the Team marker.
		"labeled": {Meta: project.Meta{ID: "labeled", Tags: []string{"agent"}}},
	}
	ids := []string{"plain", "captain", "hunter", "shown", "mine", "labeled"}
	if got := withoutAgentProjects(ids, projects, ""); !reflect.DeepEqual(got, []string{"plain", "shown", "mine", "labeled"}) {
		t.Errorf("hidden list = %v", got)
	}
	// The project being viewed keeps its row.
	if got := withoutAgentProjects(ids, projects, "hunter"); !reflect.DeepEqual(got, []string{"plain", "hunter", "shown", "mine", "labeled"}) {
		t.Errorf("scoped list = %v", got)
	}
}
