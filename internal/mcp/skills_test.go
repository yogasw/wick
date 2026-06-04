package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

// overrideHome temporarily redirects os.UserHomeDir via HOME/USERPROFILE
// so KnownDirs() sees our temp dirs instead of the real ~/.claude/skills etc.
func overrideHome(t *testing.T, dir string) {
	t.Helper()
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	_ = origHome
	_ = origUserProfile
}

// makeSkillDir creates a provider skill dir and returns its path.
func makeSkillDir(t *testing.T, home, provider string) string {
	t.Helper()
	d := filepath.Join(home, "."+provider, "skills")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// writeSkillFolder creates a skill folder with a SKILL.md inside.
func writeSkillFolder(t *testing.T, dir, name, content string) {
	t.Helper()
	folder := filepath.Join(dir, name)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWickSkillList_returnsSkillsWithMeta(t *testing.T) {
	tmp := t.TempDir()
	overrideHome(t, tmp)

	claudeDir := makeSkillDir(t, tmp, "claude")
	codexDir := makeSkillDir(t, tmp, "codex")
	_ = codexDir // codex exists but skill is missing from it

	writeSkillFolder(t, claudeDir, "example-skill", `---
name: example-skill
description: "Example skill description"
version: 1.0.0
---
`)

	h := NewHandler(nil)
	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_skill_list",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody=%s", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("unexpected isError=true:\n%s", raw)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("empty content in response:\n%s", raw)
	}

	var payload struct {
		Providers []skillsync.ProviderLocation `json:"providers"`
		Skills    []skillsync.SkillInfo        `json:"skills"`
		Total     int                          `json:"total"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v\ntext=%s", err, resp.Result.Content[0].Text)
	}

	if payload.Total != 1 {
		t.Fatalf("total = %d, want 1", payload.Total)
	}
	if len(payload.Providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(payload.Providers))
	}

	skill := payload.Skills[0]
	if skill.Name != "example-skill" {
		t.Fatalf("skill.name = %q, want %q", skill.Name, "example-skill")
	}
	if !skill.IsDir {
		t.Fatal("skill.is_dir = false, want true")
	}
	if skill.Meta["name"] != "example-skill" {
		t.Fatalf("meta.name = %q, want %q", skill.Meta["name"], "example-skill")
	}
	if skill.Meta["description"] != "Example skill description" {
		t.Fatalf("meta.description = %q", skill.Meta["description"])
	}
	if skill.Meta["version"] != "1.0.0" {
		t.Fatalf("meta.version = %q, want 1.0.0", skill.Meta["version"])
	}

	if len(skill.InProviders) != 1 || skill.InProviders[0].Label != "claude" {
		t.Fatalf("in_providers = %+v, want [{claude}]", skill.InProviders)
	}
	if len(skill.MissingProviders) != 1 || skill.MissingProviders[0].Label != "codex" {
		t.Fatalf("missing_providers = %+v, want [{codex}]", skill.MissingProviders)
	}
}

func TestWickSkillList_multiplProviders(t *testing.T) {
	tmp := t.TempDir()
	overrideHome(t, tmp)

	claudeDir := makeSkillDir(t, tmp, "claude")
	codexDir := makeSkillDir(t, tmp, "codex")

	// same skill in both providers
	skillMD := `---
name: wick-workflow
description: Build and run wick workflows
trigger: /wick-workflow
---
`
	writeSkillFolder(t, claudeDir, "wick-workflow", skillMD)
	writeSkillFolder(t, codexDir, "wick-workflow", skillMD)

	h := NewHandler(nil)
	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_skill_list",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var payload struct {
		Skills []skillsync.SkillInfo `json:"skills"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	skill := payload.Skills[0]
	if len(skill.InProviders) != 2 {
		t.Fatalf("in_providers = %d, want 2", len(skill.InProviders))
	}
	if len(skill.MissingProviders) != 0 {
		t.Fatalf("missing_providers = %d, want 0", len(skill.MissingProviders))
	}
	if skill.Meta["trigger"] != "/wick-workflow" {
		t.Fatalf("meta.trigger = %q, want /wick-workflow", skill.Meta["trigger"])
	}
}

func TestWickSkillSync_copiesAcrossProviders(t *testing.T) {
	tmp := t.TempDir()
	overrideHome(t, tmp)

	claudeDir := makeSkillDir(t, tmp, "claude")
	codexDir := makeSkillDir(t, tmp, "codex")

	// Sync only copies flat files (folders use skillEntrySync separately).
	// Write a flat .md skill file only in claude.
	content := `---
name: my-skill
description: Test skill
---
`
	src := filepath.Join(claudeDir, "my-skill.md")
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(nil)
	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_skill_sync",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true:\n%s", raw)
	}

	var payload struct {
		Copied    int      `json:"copied"`
		Skipped   int      `json:"skipped"`
		Errors    []string `json:"errors"`
		Providers []struct {
			Label string `json:"label"`
			Dir   string `json:"dir"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Copied == 0 {
		t.Fatalf("copied = 0, expected at least 1 file synced to codex")
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("errors = %v, want none", payload.Errors)
	}

	// verify file actually landed in codex
	synced := filepath.Join(codexDir, "my-skill.md")
	if _, err := os.Stat(synced); os.IsNotExist(err) {
		t.Fatalf("my-skill.md not found in codex after sync: %s", synced)
	}
}

func TestWickSkillList_toolMetadata(t *testing.T) {
	tmp := t.TempDir()
	overrideHome(t, tmp)

	claudeDir := makeSkillDir(t, tmp, "claude")
	toolMD := filepath.Join(claudeDir, "grafana-loki")
	if err := os.MkdirAll(toolMD, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolMD, "TOOL.md"), []byte(`---
name: grafana-loki
type: tool
status: no-skill
---
`), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(nil)
	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_skill_list",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var payload struct {
		Skills []skillsync.SkillInfo `json:"skills"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Skills) == 0 {
		t.Fatal("no skills returned")
	}
	skill := payload.Skills[0]
	if skill.Meta["type"] != "tool" {
		t.Fatalf("meta.type = %q, want tool", skill.Meta["type"])
	}
	if skill.Meta["status"] != "no-skill" {
		t.Fatalf("meta.status = %q, want no-skill", skill.Meta["status"])
	}
}
