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
		"mine":    {Meta: project.Meta{ID: "mine", Tags: []string{project.PersonalTag}}},
	}
	ids := []string{"plain", "captain", "hunter", "mine"}
	if got := withoutAgentProjects(ids, projects, ""); !reflect.DeepEqual(got, []string{"plain", "mine"}) {
		t.Errorf("hidden list = %v", got)
	}
	// The project being viewed keeps its row.
	if got := withoutAgentProjects(ids, projects, "hunter"); !reflect.DeepEqual(got, []string{"plain", "hunter", "mine"}) {
		t.Errorf("scoped list = %v", got)
	}
}
