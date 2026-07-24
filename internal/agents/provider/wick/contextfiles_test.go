package wick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadContextFiles_AgentAndMemory loads AGENT.md + memory.md + skills.md
// with their section headers.
func TestLoadContextFiles_AgentAndMemory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENT.md", "use tabs not spaces")
	writeFile(t, dir, "memory.md", "the user prefers Go")
	writeFile(t, dir, "skills.md", "deploy: run make ship")

	out := loadContextFiles(dir)
	for _, want := range []string{"use tabs not spaces", "the user prefers Go", "deploy: run make ship",
		"Project instructions (AGENT.md)", "Memory (memory.md)", "Skills (skills.md)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

// TestLoadContextFiles_AgentSlotExclusive: only the first AGENT-style file
// is used (AGENT.md wins over AGENTS.md / CLAUDE.md) so duplicate
// instructions aren't injected three times.
func TestLoadContextFiles_AgentSlotExclusive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENT.md", "PRIMARY")
	writeFile(t, dir, "AGENTS.md", "SECONDARY")
	writeFile(t, dir, "CLAUDE.md", "TERTIARY")

	out := loadContextFiles(dir)
	if !strings.Contains(out, "PRIMARY") {
		t.Errorf("AGENT.md should be used: %s", out)
	}
	if strings.Contains(out, "SECONDARY") || strings.Contains(out, "TERTIARY") {
		t.Errorf("only the first AGENT-style file should load: %s", out)
	}
}

// TestLoadContextFiles_FallbackToClaude: with no AGENT.md/AGENTS.md, CLAUDE.md fills the slot.
func TestLoadContextFiles_FallbackToClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "CLAUDE.md", "claude rules")
	out := loadContextFiles(dir)
	if !strings.Contains(out, "claude rules") {
		t.Errorf("CLAUDE.md should fill the agent slot when it's the only one: %s", out)
	}
}

// TestLoadContextFiles_None: empty dir / empty path → "".
func TestLoadContextFiles_None(t *testing.T) {
	if out := loadContextFiles(t.TempDir()); out != "" {
		t.Errorf("empty dir should yield no context, got %q", out)
	}
	if out := loadContextFiles(""); out != "" {
		t.Errorf("empty path should yield no context, got %q", out)
	}
}

// TestReadCapped_Truncates: a file over the cap is truncated with a marker.
func TestReadCapped_Truncates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENT.md", strings.Repeat("x", contextFileMaxBytes+500))
	out := readCapped(filepath.Join(dir, "AGENT.md"))
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation marker")
	}
	if len(out) > contextFileMaxBytes+64 {
		t.Errorf("not truncated: len=%d", len(out))
	}
}
