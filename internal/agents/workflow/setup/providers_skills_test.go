package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow/provider"
)

func writeSkillFixture(t *testing.T, home, providerDir, name string) {
	t.Helper()
	dir := filepath.Join(home, providerDir, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\nname: " + name + "\ndescription: test skill\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func skillNames(skills []provider.Skill) map[string]bool {
	out := map[string]bool{}
	for _, s := range skills {
		out[s.Name] = true
	}
	return out
}

func TestSkillsForProvider_ListsSkillInProviderDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, home, ".claude", "bitbucket-pr-review")

	names := skillNames(skillsForProvider("claude"))
	if !names["bitbucket-pr-review"] {
		t.Fatalf("claude should list bitbucket-pr-review, got %v", names)
	}
}

func TestSkillsForProvider_SharedAgentsSkillVisible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, home, ".agents", "shared-skill")

	names := skillNames(skillsForProvider("claude"))
	if !names["shared-skill"] {
		t.Fatalf("claude should see shared .agents skill, got %v", names)
	}
}

func TestSkillsForProvider_OtherProviderSkillExcluded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, home, ".codex", "codex-only")

	names := skillNames(skillsForProvider("claude"))
	if names["codex-only"] {
		t.Fatalf("claude must not list a codex-only skill, got %v", names)
	}
}
