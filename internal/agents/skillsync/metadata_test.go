package skillsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name: "full frontmatter",
			input: `---
name: example-skill
description: "Example skill description"
version: 1.0.0
type: skill
trigger: /example-skill
status: active
---

rest of file`,
			want: map[string]string{
				"name":        "example-skill",
				"description": "Example skill description",
				"version":     "1.0.0",
				"type":        "skill",
				"trigger":     "/example-skill",
				"status":      "active",
			},
		},
		{
			name: "single quotes description",
			input: `---
name: waflow-docs
description: 'Meta WhatsApp Flows docs'
version: 2.0.0
---`,
			want: map[string]string{
				"name":        "waflow-docs",
				"description": "Meta WhatsApp Flows docs",
				"version":     "2.0.0",
			},
		},
		{
			name: "tool type no-skill status",
			input: `---
name: grafana-loki
type: tool
status: no-skill
---`,
			want: map[string]string{
				"name":   "grafana-loki",
				"type":   "tool",
				"status": "no-skill",
			},
		},
		{
			name:  "no frontmatter",
			input: `just plain markdown content`,
			want:  nil,
		},
		{
			name:  "empty",
			input: ``,
			want:  nil,
		},
		{
			name: "description with colon inside",
			input: `---
name: wick-workflow
description: Build, edit, test, and run wick workflows over MCP. Use when user asks to create/modify a workflow.
trigger: /wick-workflow
---`,
			want: map[string]string{
				"name":        "wick-workflow",
				"description": "Build, edit, test, and run wick workflows over MCP. Use when user asks to create/modify a workflow.",
				"trigger":     "/wick-workflow",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFrontmatter([]byte(tt.input))
			if len(got) != len(tt.want) {
				t.Fatalf("\ngot:  %+v\nwant: %+v", got, tt.want)
			}
			for k, wantV := range tt.want {
				if got[k] != wantV {
					t.Errorf("key %q: got %q, want %q", k, got[k], wantV)
				}
			}
		})
	}
}

func TestListSkills_folder(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, ".claude", "skills")
	codex := filepath.Join(tmp, ".codex", "skills")
	for _, d := range []string{claude, codex} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	skillDir := filepath.Join(claude, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := `---
name: my-skill
description: A test skill
version: 0.1.0
---
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	meta := resolveMetaForEntry("my-skill", []string{claude, codex})
	if meta["name"] != "my-skill" {
		t.Errorf("name = %q, want %q", meta["name"], "my-skill")
	}
	if meta["description"] != "A test skill" {
		t.Errorf("description = %q, want %q", meta["description"], "A test skill")
	}
	if meta["version"] != "0.1.0" {
		t.Errorf("version = %q, want %q", meta["version"], "0.1.0")
	}
}

func TestListSkills_fileEntry(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, ".claude", "skills")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
name: single-file
type: tool
status: no-skill
---
`
	if err := os.WriteFile(filepath.Join(claude, "single-file.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta := resolveMetaForEntry("single-file.md", []string{claude})
	if meta["name"] != "single-file" {
		t.Errorf("name = %q, want %q", meta["name"], "single-file")
	}
	if meta["type"] != "tool" {
		t.Errorf("type = %q, want %q", meta["type"], "tool")
	}
	if meta["status"] != "no-skill" {
		t.Errorf("status = %q, want %q", meta["status"], "no-skill")
	}
}

func TestParseFrontmatter_TOOL_md(t *testing.T) {
	input := `---
name: grafana-loki
type: tool
status: no-skill
---`
	meta := parseFrontmatter([]byte(input))
	if meta["name"] != "grafana-loki" || meta["type"] != "tool" || meta["status"] != "no-skill" {
		t.Errorf("unexpected meta: %+v", meta)
	}
}
