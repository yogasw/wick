package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeSettings(t *testing.T) {
	bytes, err := ClaudeSettings("/path/to/gate")
	if err != nil {
		t.Fatal(err)
	}
	var got claudeHookConfig
	if err := json.Unmarshal(bytes, &got); err != nil {
		t.Fatal(err)
	}
	// Expect one group per gated tool: Bash, Read, Write, Edit, Glob.
	wantMatchers := []string{"Bash", "Read", "Write", "Edit", "Glob"}
	if len(got.Hooks.PreToolUse) != len(wantMatchers) {
		t.Fatalf("PreToolUse groups: got %d, want %d", len(got.Hooks.PreToolUse), len(wantMatchers))
	}
	for i, want := range wantMatchers {
		g := got.Hooks.PreToolUse[i]
		if g.Matcher != want {
			t.Errorf("group[%d] matcher: got %q, want %q", i, g.Matcher, want)
		}
		if len(g.Hooks) != 1 || g.Hooks[0].Type != "command" || g.Hooks[0].Command != "/path/to/gate" {
			t.Errorf("group[%d] hook entry: %+v", i, g.Hooks)
		}
	}
}

func TestWriteClaudeSettings(t *testing.T) {
	dir := t.TempDir()
	settings, err := WriteClaudeSettings(dir, "/bin/gate")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(settings) != dir {
		t.Errorf("settings path: %q want under %q", settings, dir)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/bin/gate") {
		t.Errorf("settings missing gate bin: %s", data)
	}
}

// TestSharedSpecRoundtrip: WriteSharedSpec + LoadSpec via temp HOME
// — fakes os.UserHomeDir() through HOME env so paths land under
// t.TempDir().
func TestSharedSpecRoundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows
	app := "testapp"
	want := Spec{
		Rules:        []CommandRule{{Pattern: "ls *"}, {Pattern: "git status"}},
		AutoApproved: []string{"hash-a", "hash-b"},
	}
	if err := WriteSharedSpec(app, want); err != nil {
		t.Fatalf("WriteSharedSpec: %v", err)
	}
	got, err := LoadSpec(app)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if len(got.Rules) != 2 || got.Rules[0].Pattern != "ls *" {
		t.Errorf("rules: %+v", got.Rules)
	}
	if len(got.AutoApproved) != 2 || got.AutoApproved[0] != "hash-a" || got.AutoApproved[1] != "hash-b" {
		t.Errorf("AutoApproved: %+v", got.AutoApproved)
	}
}

// TestLoadSpecMissingIsEmpty: a missing shared spec returns an
// empty Spec, no error — matcher then treats every command as
// non-whitelisted (fail-safe block via daemon socket).
func TestLoadSpecMissingIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	got, err := LoadSpec("never-written")
	if err != nil {
		t.Fatalf("LoadSpec on missing should not error, got: %v", err)
	}
	if len(got.Rules) != 0 || len(got.AutoApproved) != 0 {
		t.Errorf("expected empty spec, got: %+v", got)
	}
}

func TestSpecApprovalFieldsOmitEmpty(t *testing.T) {
	bytes, err := json.Marshal(Spec{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(bytes)
	if strings.Contains(s, "auto_approved") {
		t.Errorf("expected auto_approved omitted, got: %s", s)
	}
}
