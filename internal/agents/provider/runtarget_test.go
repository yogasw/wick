package provider

import "testing"

func TestResolveRunTargetPicksFirstNamedProvider(t *testing.T) {
	got := ResolveRunTarget(
		RunTarget{},
		RunTarget{Provider: "wick/x", ModelID: "e1@gpt-5"},
		RunTarget{Provider: "claude", ModelID: "opus"},
	)
	if got.Provider != "wick/x" || got.ModelID != "e1@gpt-5" {
		t.Fatalf("got %+v, want wick/x + e1@gpt-5", got)
	}
}

// The whole point of the type: a winning candidate with no model must NOT
// borrow one from a weaker candidate. That model belongs to a different
// instance, and running it against this one resolves to the wrong model
// or to nothing.
func TestResolveRunTargetDoesNotBackfillModelFromWeakerCandidate(t *testing.T) {
	got := ResolveRunTarget(
		RunTarget{Provider: "codex"},
		RunTarget{Provider: "wick/x", ModelID: "e1@gpt-5"},
	)
	if got.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", got.Provider)
	}
	if got.ModelID != "" {
		t.Fatalf("model = %q, want empty — a codex spawn must not inherit a wick pin", got.ModelID)
	}
}

// A model with no provider is not a usable target: there is no instance
// to resolve the pin against, so such a candidate is skipped entirely
// rather than contributing its model to the next one.
func TestResolveRunTargetSkipsModelOnlyCandidate(t *testing.T) {
	got := ResolveRunTarget(
		RunTarget{ModelID: "orphan@model"},
		RunTarget{Provider: "claude"},
	)
	if got.Provider != "claude" || got.ModelID != "" {
		t.Fatalf("got %+v, want claude with no model", got)
	}
}

func TestResolveRunTargetTrimsAndHandlesNoCandidates(t *testing.T) {
	got := ResolveRunTarget(RunTarget{Provider: "  wick/x  ", ModelID: "  e1@m  "})
	if got.Provider != "wick/x" || got.ModelID != "e1@m" {
		t.Fatalf("got %+v, want trimmed", got)
	}
	// Whitespace-only is not a provider.
	if !ResolveRunTarget(RunTarget{Provider: "   "}).Empty() {
		t.Fatal("whitespace-only provider should be Empty")
	}
	if !ResolveRunTarget().Empty() {
		t.Fatal("no candidates should resolve to the zero target")
	}
}

func TestSplitInstanceKey(t *testing.T) {
	cases := []struct{ in, wantType, wantName string }{
		{"claude", "claude", "claude"},
		{"claude/work", "claude", "work"},
		{"wick/x", "wick", "x"},
		{" claude ", "claude", "claude"},
		// A trailing slash names an empty instance name; preserved rather
		// than guessed at, so a malformed key fails a Find rather than
		// silently resolving to the canonical instance.
		{"claude/", "claude", ""},
	}
	for _, c := range cases {
		typ, name := SplitInstanceKey(c.in)
		if typ != c.wantType || name != c.wantName {
			t.Errorf("SplitInstanceKey(%q) = (%q,%q), want (%q,%q)", c.in, typ, name, c.wantType, c.wantName)
		}
	}
}

func TestSameInstanceNormalizesBareType(t *testing.T) {
	if !SameInstance("claude", "claude/claude") {
		t.Error("bare type should equal its canonical instance")
	}
	if SameInstance("wick/x", "wick/y") {
		t.Error("different instance names must not match")
	}
	if SameInstance("claude/x", "codex/x") {
		t.Error("different types must not match")
	}
}
