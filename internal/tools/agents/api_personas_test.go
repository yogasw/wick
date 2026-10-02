package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/persona"
	"github.com/yogasw/wick/internal/agents/project"
)

func TestPersonaValidateGrants(t *testing.T) {
	ok := []persona.ConnectorGrant{
		{ConnectorID: "a", Level: persona.LevelAll},
		{ConnectorID: "b", Level: persona.LevelRead},
		{ConnectorID: "c", Level: persona.LevelPick, Ops: []string{"get"}},
	}
	if err := validateGrants(ok); err != nil {
		t.Fatalf("valid grants rejected: %v", err)
	}
	if err := validateGrants([]persona.ConnectorGrant{{ConnectorID: "", Level: persona.LevelAll}}); err == nil {
		t.Fatal("empty connector_id accepted")
	}
	if err := validateGrants([]persona.ConnectorGrant{{ConnectorID: "a", Level: "write"}}); err == nil {
		t.Fatal("unknown level accepted")
	}
}

func TestPersonaApplyProjectFields(t *testing.T) {
	s := func(v string) *string { return &v }
	m := project.Meta{Name: "Old", Icon: "📁", Defaults: project.Defaults{Provider: "claude/claude", Model: "m1"}}

	if applyProjectFields(&m, personaWriteReq{}) {
		t.Fatal("empty request must not report a change")
	}
	// A blank name/icon is ignored rather than wiping the project's.
	if applyProjectFields(&m, personaWriteReq{Name: s("  "), Icon: s("")}) || m.Name != "Old" {
		t.Fatalf("blank name applied: %+v", m)
	}
	if !applyProjectFields(&m, personaWriteReq{Name: s(" New "), SystemPrompt: s("be brief")}) {
		t.Fatal("change not reported")
	}
	if m.Name != "New" || m.Defaults.SystemAddon != "be brief" {
		t.Fatalf("fields not applied: %+v", m)
	}
	// Switching provider without a model drops the old pin.
	applyProjectFields(&m, personaWriteReq{Provider: s("codex/codex")})
	if m.Defaults.Provider != "codex/codex" || m.Defaults.Model != "" {
		t.Fatalf("model pin not dropped: %+v", m.Defaults)
	}
	applyProjectFields(&m, personaWriteReq{Model: s("gpt-x")})
	if m.Defaults.Model != "gpt-x" {
		t.Fatalf("model not set: %+v", m.Defaults)
	}
}
