package delegation

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

func sessionTargets(m map[string]provider.RunTarget) SessionRunTarget {
	return func(id string) (provider.RunTarget, bool) {
		t, ok := m[id]
		return t, ok
	}
}

func projectTargets(m map[string]provider.RunTarget) ProjectRunTarget {
	return func(id string) (provider.RunTarget, bool) {
		t, ok := m[id]
		return t, ok
	}
}

// The default expectation: a sub-agent spawned from a conversation runs
// where that conversation runs, down to the live-set leaf.
func TestResolveChildTargetInheritsParentSession(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "researcher"}, // no provider, no model
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "wick/x", ModelID: "set1@gemini-3.6-flash"},
		}),
		projectTargets(map[string]provider.RunTarget{
			"proj-1": {Provider: "claude", ModelID: "opus"},
		}),
	)
	if got.Provider != "wick/x" || got.ModelID != "set1@gemini-3.6-flash" {
		t.Fatalf("got %+v, want the parent session's provider AND model", got)
	}
}

// A role may own its runtime completely — provider and model, to the leaf.
func TestResolveChildTargetRolePinWinsWhole(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "r", Provider: "wick/y", Model: "set9@claude-opus-5"},
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "wick/x", ModelID: "set1@gpt-5"},
		}),
		projectTargets(map[string]provider.RunTarget{"proj-1": {Provider: "claude"}}),
	)
	if got.Provider != "wick/y" || got.ModelID != "set9@claude-opus-5" {
		t.Fatalf("got %+v, want the role's own provider+model", got)
	}
}

// The reported case: a role naming only a bare provider ("codex") while the
// parent conversation runs on that same instance with a model picked in the
// composer. The role has an opinion about the runtime and none about the
// model, so the session's model must come through — withholding it made the
// role silently override the switch.
func TestResolveChildTargetTakesParentModelOnTheSameInstance(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "test-agent", Provider: "codex"}, // bare type, no model
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "codex/codex", ModelID: "gpt-5.5"},
		}),
		nil,
	)
	if got.Provider != "codex" {
		t.Fatalf("provider = %q, want codex (the role's own pick)", got.Provider)
	}
	if got.ModelID != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5 from the session — same instance, so the pin applies", got.ModelID)
	}
}

// The role's OWN model still wins when it has one: the parent only fills a
// gap, it never overrules a stated choice.
func TestResolveChildTargetPrefersTheRolesOwnModel(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "r", Provider: "codex/codex", Model: "gpt-5.4-mini"},
		"parent-1", "",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "codex/codex", ModelID: "gpt-5.5"},
		}),
		nil,
	)
	if got.ModelID != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want the role's own gpt-5.4-mini", got.ModelID)
	}
}

// The cross-instance rule: a role that names a DIFFERENT provider must not
// inherit the parent's model pin. That pin names an entry in another
// instance's registry, so applying it here resolves to the wrong model or
// to nothing at all.
func TestResolveChildTargetRoleProviderDoesNotInheritParentModel(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "r", Provider: "codex"}, // provider only
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "wick/x", ModelID: "set1@gpt-5"},
		}),
		nil,
	)
	if got.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", got.Provider)
	}
	if got.ModelID != "" {
		t.Fatalf("model = %q, want empty — a wick pin must not reach a codex spawn", got.ModelID)
	}
}

// Falls to the project only when neither the role nor the parent answers.
func TestResolveChildTargetFallsToProject(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "r"},
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{}), // parent unknown
		projectTargets(map[string]provider.RunTarget{
			"proj-1": {Provider: "wick/x", ModelID: "set1@leaf"},
		}),
	)
	if got.Provider != "wick/x" || got.ModelID != "set1@leaf" {
		t.Fatalf("got %+v, want the project default pair", got)
	}
}

// A parent running on a provider with no model pin contributes the
// provider and stops there — it must not skip to the project for a model
// that belongs to a different instance.
func TestResolveChildTargetParentWithoutModelDoesNotBorrowProjectModel(t *testing.T) {
	got := resolveChildTarget(
		&entity.AgentProfile{Key: "r"},
		"parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{
			"parent-1": {Provider: "wick/x"},
		}),
		projectTargets(map[string]provider.RunTarget{
			"proj-1": {Provider: "wick/y", ModelID: "other@leaf"},
		}),
	)
	if got.Provider != "wick/x" {
		t.Fatalf("provider = %q, want wick/x", got.Provider)
	}
	if got.ModelID != "" {
		t.Fatalf("model = %q, want empty — wick/y's pin is not wick/x's", got.ModelID)
	}
}

// Nothing answers: the target stays empty so the spawn path applies its
// own per-type default. Notably it does NOT reach for the global
// DefaultProvider, which is a new-session setting.
func TestResolveChildTargetEmptyWhenNothingAnswers(t *testing.T) {
	got := resolveChildTarget(&entity.AgentProfile{Key: "r"}, "parent-1", "proj-1",
		sessionTargets(map[string]provider.RunTarget{}),
		projectTargets(map[string]provider.RunTarget{}))
	if !got.Empty() {
		t.Fatalf("got %+v, want an empty target", got)
	}
}

func TestResolveChildTargetToleratesNilInputs(t *testing.T) {
	if got := resolveChildTarget(nil, "", "", nil, nil); !got.Empty() {
		t.Fatalf("got %+v, want empty", got)
	}
	// A nil profile must not stop session inheritance.
	got := resolveChildTarget(nil, "p", "", sessionTargets(map[string]provider.RunTarget{
		"p": {Provider: "wick/x", ModelID: "m"},
	}), nil)
	if got.Provider != "wick/x" || got.ModelID != "m" {
		t.Fatalf("got %+v, want wick/x + m", got)
	}
}
