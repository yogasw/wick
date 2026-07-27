package agents

import "testing"

// scope=new must keep ONLY the switch actions (so `/` works pre-session even
// for a provider with no skills), and the default scope must return every
// built-in. Panels/views/send actions must never leak into scope=new.
func TestBuiltinsForScope(t *testing.T) {
	newOnes := builtinsForScope("new")
	if len(newOnes) != 2 {
		t.Fatalf("scope=new: want 2 switch actions, got %d: %+v", len(newOnes), newOnes)
	}
	for _, cmd := range newOnes {
		if cmd.Action != "switch:provider" && cmd.Action != "switch:project" {
			t.Errorf("scope=new leaked a non-switch action: %q", cmd.Action)
		}
	}

	all := builtinsForScope("")
	if len(all) != len(builtinComposerCommands) {
		t.Errorf("default scope: want all %d built-ins, got %d", len(builtinComposerCommands), len(all))
	}
}

// /thinking (panel:thinking) is wick-only: present for wick + empty provider,
// hidden for the CLI providers.
func TestFilterBuiltinsForProvider(t *testing.T) {
	has := func(cmds []ComposerCommand, action string) bool {
		for _, c := range cmds {
			if c.Action == action {
				return true
			}
		}
		return false
	}
	all := builtinsForScope("")
	if !has(filterBuiltinsForProvider(all, "wick"), "panel:thinking") {
		t.Error("wick should keep /thinking")
	}
	if !has(filterBuiltinsForProvider(all, ""), "panel:thinking") {
		t.Error("empty provider should keep /thinking (permissive default)")
	}
	if has(filterBuiltinsForProvider(all, "claude"), "panel:thinking") {
		t.Error("claude should NOT get /thinking")
	}
	// Non-thinking commands are unaffected by provider.
	if !has(filterBuiltinsForProvider(all, "claude"), "send:/compact") {
		t.Error("filtering must not drop unrelated commands like /compact")
	}
}
