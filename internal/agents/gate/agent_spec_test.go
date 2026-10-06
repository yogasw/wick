package gate

import (
	"path/filepath"
	"testing"
)

func TestAgentSpecRoundTripAndArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents", "a1.json")
	in := AgentSpec{AgentID: "a1", Rules: []CommandRule{{Pattern: "git status"}}, DefaultScope: "/p"}
	if err := WriteAgentSpec(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadAgentSpec(path)
	if err != nil || out.AgentID != "a1" || len(out.Rules) != 1 || out.DefaultScope != "/p" {
		t.Fatalf("load = %+v, %v", out, err)
	}
	if _, err := LoadAgentSpec(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing agent spec must be an error, not an empty spec")
	}
	cmd := HookCommand("/bin/wick-gate", path)
	if got := SpecArg([]string{SpecFlag, path}); got != path {
		t.Fatalf("SpecArg = %q", got)
	}
	if got := SpecArg([]string{SpecFlag + "=/x.json"}); got != "/x.json" {
		t.Fatalf("SpecArg = %q", got)
	}
	if cmd != "/bin/wick-gate --spec "+filepath.ToSlash(path) {
		t.Fatalf("HookCommand = %q", cmd)
	}
	if HookCommand("/bin/g", "") != "/bin/g" || HookCommand("/bin/g", "/a b.json") != "/bin/g --spec '/a b.json'" {
		t.Fatal("HookCommand quoting")
	}
}

// An agent spec with no rules matches nothing, so every command goes to
// the approval prompt rather than running unasked.
func TestAgentSpecEmptyRulesAsksForEverything(t *testing.T) {
	m := NewMatcher(nil, "/p")
	if allow, _ := m.Decide("ls"); allow {
		t.Fatal("empty agent rules allowed a command")
	}
	m = NewMatcher([]CommandRule{{Pattern: "cat *", Scope: "/p"}}, "/p")
	if allow, _ := m.Decide("cat /p/a.txt"); !allow {
		t.Fatal("in-scope command blocked")
	}
	if allow, _ := m.Decide("cat /etc/passwd"); allow {
		t.Fatal("out-of-scope command allowed")
	}
}
