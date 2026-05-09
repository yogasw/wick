package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeSettings(t *testing.T) {
	bytes, err := ClaudeSettings("/path/to/wick-gate")
	if err != nil {
		t.Fatal(err)
	}
	var got claudeHookConfig
	if err := json.Unmarshal(bytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hooks.PreToolUse) != 1 {
		t.Fatalf("PreToolUse groups: %d", len(got.Hooks.PreToolUse))
	}
	g := got.Hooks.PreToolUse[0]
	if g.Matcher != "Bash" {
		t.Errorf("matcher: %q", g.Matcher)
	}
	if len(g.Hooks) != 1 || g.Hooks[0].Type != "command" || g.Hooks[0].Command != "/path/to/wick-gate" {
		t.Errorf("hook entry: %+v", g.Hooks)
	}
}

func TestWriteSpawnArtifactsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{
		SessionID: "S1",
		AgentName: "default",
		Layout:    SpecLayout{SessionCommandsPath: filepath.Join(dir, "commands.jsonl")},
		Rules:     []CommandRule{{Pattern: "ls *"}},
	}

	settings, specPath, err := WriteSpawnArtifacts(dir, spec, "/bin/wick-gate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	if _, err := os.Stat(specPath); err != nil {
		t.Fatalf("spec.json missing: %v", err)
	}

	// LoadSpec round-trip via env var.
	t.Setenv(HookEnvVar, specPath)
	got, err := LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "S1" || got.AgentName != "default" {
		t.Fatalf("spec: %+v", got)
	}
	if len(got.Rules) != 1 || got.Rules[0].Pattern != "ls *" {
		t.Fatalf("rules: %+v", got.Rules)
	}
}

func TestLoadSpecMissingEnv(t *testing.T) {
	t.Setenv(HookEnvVar, "")
	if _, err := LoadSpec(); err == nil {
		t.Fatal("expected error when env var unset")
	}
}
