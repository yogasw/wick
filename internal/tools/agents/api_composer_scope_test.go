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
