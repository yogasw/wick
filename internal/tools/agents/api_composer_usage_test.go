package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// The `/usage` command takes the provider as "type/name", and a bare
// type has to keep working: most installs have one instance whose name
// equals its type, and the composer passes whatever the session holds.
func TestSplitProviderKey(t *testing.T) {
	cases := []struct {
		in       string
		wantType provider.Type
		wantName string
		wantOK   bool
	}{
		{in: "claude/enginer", wantType: provider.TypeClaude, wantName: "enginer", wantOK: true},
		{in: "claude", wantType: provider.TypeClaude, wantName: "claude", wantOK: true},
		{in: " codex/codex ", wantType: provider.TypeCodex, wantName: "codex", wantOK: true},
		// A trailing slash names no instance; fall back to the type so the
		// lookup fails on a real name rather than on an empty one.
		{in: "claude/", wantType: provider.TypeClaude, wantName: "claude", wantOK: true},
		{in: "", wantOK: false},
		{in: "   ", wantOK: false},
	}
	for _, tc := range cases {
		gotType, gotName, ok := splitProviderKey(tc.in)
		if ok != tc.wantOK {
			t.Errorf("splitProviderKey(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if gotType != tc.wantType || gotName != tc.wantName {
			t.Errorf("splitProviderKey(%q) = %q/%q, want %q/%q", tc.in, gotType, gotName, tc.wantType, tc.wantName)
		}
	}
}

// /usage must be offered in the menu, and must open a popover rather
// than sending a message — a "send:" action would post "/usage" into the
// conversation, which is not what the user asked for.
func TestUsageCommandIsAPanelAction(t *testing.T) {
	var found *ComposerCommand
	for i := range builtinComposerCommands {
		if builtinComposerCommands[i].ID == "usage" {
			found = &builtinComposerCommands[i]
			break
		}
	}
	if found == nil {
		t.Fatal("/usage missing from the composer menu")
	}
	if found.Label != "/usage" {
		t.Errorf("label = %q", found.Label)
	}
	if found.Action != "panel:usage" {
		t.Errorf("action = %q, want panel:usage", found.Action)
	}
	if found.Insert != "" {
		t.Errorf("insert = %q, want empty — /usage is an action, not a skill", found.Insert)
	}
}
