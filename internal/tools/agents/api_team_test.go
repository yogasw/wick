package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/project"
)

func TestTeamAgentValidateGrants(t *testing.T) {
	ok := []team.ConnectorGrant{
		{ConnectorID: "a", Level: team.LevelAll},
		{ConnectorID: "b", Level: team.LevelRead},
		{ConnectorID: "c", Level: team.LevelPick, Ops: []string{"get"}},
	}
	if err := validateGrants(ok); err != nil {
		t.Fatalf("valid grants rejected: %v", err)
	}
	if err := validateGrants([]team.ConnectorGrant{{ConnectorID: "", Level: team.LevelAll}}); err == nil {
		t.Fatal("empty connector_id accepted")
	}
	if err := validateGrants([]team.ConnectorGrant{{ConnectorID: "a", Level: "write"}}); err == nil {
		t.Fatal("unknown level accepted")
	}
}

func TestTeamAgentApplyProjectFields(t *testing.T) {
	s := func(v string) *string { return &v }
	m := project.Meta{Name: "Old", Icon: "📁", Defaults: project.Defaults{Provider: "claude/claude", Model: "m1"}}

	if applyProjectFields(&m, teamAgentWriteReq{}) {
		t.Fatal("empty request must not report a change")
	}
	// A blank name/icon is ignored rather than wiping the project's.
	if applyProjectFields(&m, teamAgentWriteReq{Name: s("  "), Icon: s("")}) || m.Name != "Old" {
		t.Fatalf("blank name applied: %+v", m)
	}
	if !applyProjectFields(&m, teamAgentWriteReq{Name: s(" New "), SystemPrompt: s("be brief")}) {
		t.Fatal("change not reported")
	}
	if m.Name != "New" || m.Defaults.SystemAddon != "be brief" {
		t.Fatalf("fields not applied: %+v", m)
	}
	// Switching provider without a model drops the old pin.
	applyProjectFields(&m, teamAgentWriteReq{Provider: s("codex/codex")})
	if m.Defaults.Provider != "codex/codex" || m.Defaults.Model != "" {
		t.Fatalf("model pin not dropped: %+v", m.Defaults)
	}
	applyProjectFields(&m, teamAgentWriteReq{Model: s("gpt-x")})
	if m.Defaults.Model != "gpt-x" {
		t.Fatalf("model not set: %+v", m.Defaults)
	}
}
