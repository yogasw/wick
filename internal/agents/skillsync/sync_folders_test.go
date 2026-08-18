package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/appname"
)

// writeSkill creates <dir>/<name>/SKILL.md (plus any extra files) with the
// given mtime, so a test can stage a realistic folder skill.
func writeSkill(t *testing.T, dir, name, body string, mtime time.Time, extra map[string]string) {
	t.Helper()
	root := filepath.Join(dir, name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	files := map[string]string{"SKILL.md": body}
	for k, v := range extra {
		files[k] = v
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
}

// stageHome sets up a temp home with ~/.claude/skills present (a third-party
// provider dir only counts when it already exists) and returns the claude and
// wick dirs.
func stageHome(t *testing.T) (home, claudeDir, wickDir string) {
	t.Helper()
	home = t.TempDir()
	setTestHome(t, home)
	claudeDir = filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude: %v", err)
	}
	wickDir = filepath.Join(home, "."+appname.Resolve(), "skills")
	return home, claudeDir, wickDir
}

// TestSyncCopiesFolderSkills is the core regression: skills are FOLDERS holding
// a SKILL.md, and Sync must mirror the whole tree. It previously skipped every
// directory entry, so the wick dir stayed empty and the provider's `/` menu
// (which requires a physical copy under the wick dir) showed no skills at all.
func TestSyncCopiesFolderSkills(t *testing.T) {
	_, claudeDir, wickDir := stageHome(t)

	mtime := time.Now().Add(-time.Hour)
	writeSkill(t, claudeDir, "my-skill", "---\nname: my-skill\n---\nbody\n", mtime, map[string]string{
		"references/extra.md": "nested\n",
	})

	res, err := Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("Sync errors: %v", res.Errors)
	}

	// The SKILL.md must land in the wick dir...
	got, err := os.ReadFile(filepath.Join(wickDir, "my-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md not synced to wick dir: %v", err)
	}
	if !strings.Contains(string(got), "name: my-skill") {
		t.Errorf("synced SKILL.md content wrong: %q", got)
	}
	// ...and so must nested files, or a skill that splits its body across
	// reference files arrives broken.
	if _, err := os.ReadFile(filepath.Join(wickDir, "my-skill", "references", "extra.md")); err != nil {
		t.Errorf("nested file not synced: %v", err)
	}
	// SkillsCopied reports skills, not the loose files that share the dir.
	if res.SkillsCopied != 1 {
		t.Errorf("SkillsCopied = %d, want 1", res.SkillsCopied)
	}
}

// TestSyncFolderNewestWins: when both dirs hold the same skill, the newer copy
// must overwrite the older one — the documented conflict rule, now applied
// per-file inside a folder.
func TestSyncFolderNewestWins(t *testing.T) {
	_, claudeDir, wickDir := stageHome(t)

	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-time.Minute)
	writeSkill(t, claudeDir, "dup", "old body\n", old, nil)
	writeSkill(t, wickDir, "dup", "new body\n", recent, nil)

	if _, err := Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(claudeDir, "dup", "SKILL.md"))
	if err != nil {
		t.Fatalf("read claude copy: %v", err)
	}
	if strings.TrimSpace(string(got)) != "new body" {
		t.Errorf("older copy won: got %q, want %q", got, "new body")
	}
}

// TestSyncSkipsUpToDateFolders: a second Sync with nothing changed must copy
// nothing, so the button is idempotent and the count stays honest.
func TestSyncSkipsUpToDateFolders(t *testing.T) {
	_, claudeDir, _ := stageHome(t)
	writeSkill(t, claudeDir, "stable", "body\n", time.Now().Add(-time.Hour), nil)

	if _, err := Sync(); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	res, err := Sync()
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if res.Copied != 0 {
		t.Errorf("second Sync copied %d files, want 0", res.Copied)
	}
	if res.SkillsCopied != 0 {
		t.Errorf("second Sync SkillsCopied = %d, want 0", res.SkillsCopied)
	}
}

// TestSyncIgnoresLooseFilesInSkillCount: CLAUDE.md / README.md and friends sit
// in the skills dir but are not skills. They still sync (harmless), yet must not
// inflate SkillsCopied — the reason Sync looked successful while copying zero
// actual skills.
func TestSyncIgnoresLooseFilesInSkillCount(t *testing.T) {
	_, claudeDir, wickDir := stageHome(t)

	mtime := time.Now().Add(-time.Hour)
	loose := filepath.Join(claudeDir, "README.md")
	if err := os.WriteFile(loose, []byte("readme\n"), 0o644); err != nil {
		t.Fatalf("write loose: %v", err)
	}
	if err := os.Chtimes(loose, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	res, err := Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.SkillsCopied != 0 {
		t.Errorf("SkillsCopied = %d for a loose file, want 0", res.SkillsCopied)
	}
	if _, err := os.Stat(filepath.Join(wickDir, "README.md")); err != nil {
		t.Errorf("loose file should still sync: %v", err)
	}
}

// TestSyncSkipsDotEntries: dotfiles/dotfolders are provider bookkeeping
// (.git, .DS_Store) and must never be mirrored.
func TestSyncSkipsDotEntries(t *testing.T) {
	_, claudeDir, wickDir := stageHome(t)

	dotDir := filepath.Join(claudeDir, ".git")
	if err := os.MkdirAll(dotDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dotDir, "HEAD"), []byte("ref\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}

	if _, err := Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wickDir, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git was mirrored into the wick dir")
	}
}

// TestInProviderMapsWickToAppName is the fix for the reported symptom: a skill
// present in wick's own dir must count for provider type "wick" even when the
// dir is labelled with a dev app name like "wick-lab". Comparing provider type
// to dir label directly returned false, so the composer `/` menu and the
// workflow skill list came back empty for the wick provider.
func TestInProviderMapsWickToAppName(t *testing.T) {
	own := OwnLabel()
	if own == "" {
		t.Fatal("OwnLabel is empty")
	}
	s := SkillInfo{
		Name:  "my-skill",
		IsDir: true,
		InProviders: []ProviderLocation{
			{Label: own, Dir: "/x/." + own + "/skills"},
		},
	}
	if !InProvider(s, "wick") {
		t.Errorf("InProvider(_, %q) = false for a skill in the %q dir", "wick", own)
	}
	if InProvider(s, "claude") {
		t.Error("skill only in wick's dir must not count for claude")
	}

	// A dev label must resolve through the same mapping.
	if got := DirLabelForProvider("wick"); got != own {
		t.Errorf("DirLabelForProvider(\"wick\") = %q, want %q", got, own)
	}
	if got := DirLabelForProvider("claude"); got != "claude" {
		t.Errorf("DirLabelForProvider(\"claude\") = %q, want \"claude\"", got)
	}
}

// TestInProviderAgentsDirIsShared: ~/.agents/skills is common ground, so a skill
// there is available to every provider.
func TestInProviderAgentsDirIsShared(t *testing.T) {
	s := SkillInfo{
		Name:        "common",
		IsDir:       true,
		InProviders: []ProviderLocation{{Label: "agents", Dir: "/x/.agents/skills"}},
	}
	for _, p := range []string{"wick", "claude", "codex", "gemini"} {
		if !InProvider(s, p) {
			t.Errorf("agents-dir skill should count for %q", p)
		}
	}
}
