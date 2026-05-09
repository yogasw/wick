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
	if len(got.Hooks.PreToolUse) != 1 {
		t.Fatalf("PreToolUse groups: %d", len(got.Hooks.PreToolUse))
	}
	g := got.Hooks.PreToolUse[0]
	if g.Matcher != "Bash" {
		t.Errorf("matcher: %q", g.Matcher)
	}
	if len(g.Hooks) != 1 || g.Hooks[0].Type != "command" || g.Hooks[0].Command != "/path/to/gate" {
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

	settings, specPath, err := WriteSpawnArtifacts(dir, spec, "/bin/gate")
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

func TestSpecApprovalFields(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{
		SessionID:    "S1",
		AgentName:    "default",
		Layout:       SpecLayout{SessionCommandsPath: filepath.Join(dir, "commands.jsonl")},
		Rules:        []CommandRule{{Pattern: "ls *"}},
		SocketPath:   filepath.Join(dir, "gate.sock"),
		AutoApproved: []string{"hash-a", "hash-b"},
	}

	_, specPath, err := WriteSpawnArtifacts(dir, spec, "/bin/gate")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(HookEnvVar, specPath)

	got, err := LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if got.SocketPath != spec.SocketPath {
		t.Errorf("SocketPath: got %q, want %q", got.SocketPath, spec.SocketPath)
	}
	if len(got.AutoApproved) != 2 || got.AutoApproved[0] != "hash-a" || got.AutoApproved[1] != "hash-b" {
		t.Errorf("AutoApproved: %+v", got.AutoApproved)
	}
}

func TestSpecApprovalFieldsOmitEmpty(t *testing.T) {
	bytes, err := json.Marshal(Spec{SessionID: "S1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(bytes)
	if strings.Contains(s, "socket_path") {
		t.Errorf("expected socket_path omitted, got: %s", s)
	}
	if strings.Contains(s, "auto_approved") {
		t.Errorf("expected auto_approved omitted, got: %s", s)
	}
}
