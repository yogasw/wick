package wick

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/skillsync"
	"github.com/yogasw/wick/internal/appname"
)

func setTestHome(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	// The skills dir hangs off appname.DataDir(), which memoizes the home
	// dir on first use — deliberately, so one process can't split its
	// writes across two trees. Drop that cache on both sides of the test
	// or the catalog scans the real home instead of dir.
	appname.ResetDataDirForTest()
	t.Cleanup(appname.ResetDataDirForTest)
}

func writeSkill(t *testing.T, home, name, desc string) {
	t.Helper()
	dir := filepath.Join(home, "."+appname.Resolve(), "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSkillCatalog_NoUserSkills: with no user skills installed the catalog is
// still non-empty, because wick ships its own. It must contain only built-ins.
//
// This replaces an older "empty home → empty catalog" expectation, which stopped
// describing the system once shipped skills existed.
func TestSkillCatalog_NoUserSkills(t *testing.T) {
	setTestHome(t, t.TempDir())

	got := skillCatalog()
	if got == "" {
		t.Fatal("catalog is empty — the shipped skills are missing")
	}
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if !strings.Contains(line, builtinTag) {
			t.Errorf("non-builtin entry with no user skills installed: %s", line)
		}
	}
}

func TestSkillCatalog_ListsNameDescPath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	writeSkill(t, home, "tool-module", "Module conventions for tools and jobs.")
	writeSkill(t, home, "connector-module", "How to build a connector.")

	cat := skillCatalog()
	if !strings.Contains(cat, "## Available skills") {
		t.Fatalf("missing heading: %q", cat)
	}
	// Both skills, with description + a SKILL.md path the agent can read.
	for _, name := range []string{"tool-module", "connector-module"} {
		if !strings.Contains(cat, "**"+name+"**") {
			t.Errorf("catalog missing skill %q:\n%s", name, cat)
		}
	}
	if !strings.Contains(cat, "Module conventions for tools and jobs.") {
		t.Errorf("catalog missing description:\n%s", cat)
	}
	if !strings.Contains(cat, "SKILL.md)") {
		t.Errorf("catalog missing SKILL.md path:\n%s", cat)
	}
	// Read-on-demand instruction present.
	if !strings.Contains(cat, "read_file") {
		t.Errorf("catalog missing read_file hint:\n%s", cat)
	}
}

// TestSkillCatalog_LongDescriptionsKeepEverySkill: this repo's skills carry very
// long descriptions (one is ~1.5KB on its own), so truncating the LIST dropped
// whole skills — always the alphabetical tail, which is why `wick-*` / `yoga-*`
// went missing from the agent's catalog. Descriptions are trimmed per-skill
// instead, so every skill stays listed.
func TestSkillCatalog_LongDescriptionsKeepEverySkill(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	huge := strings.Repeat("verbose skill description that goes on and on. ", 60) // ~2.8KB each
	names := []string{"aaa-first", "mmm-middle", "zzz-last"}
	for _, n := range names {
		writeSkill(t, home, n, huge)
	}

	got := skillCatalog()
	for _, n := range names {
		if !strings.Contains(got, n) {
			t.Errorf("skill %q missing from catalog (len %d):\n%s", n, len(got), got)
		}
	}
	if len(got) > skillCatalogMaxBytes {
		t.Errorf("catalog %d bytes exceeds cap %d", len(got), skillCatalogMaxBytes)
	}
}

// TestSkillCatalog_PrefersAppNameDir: the catalog must point at the skill copy
// under wick's OWN data dir, whose DirLabel is the resolved app name — "wick" in
// prod but "wick-lab" for a dev build. Matching the literal "wick" made dev
// builds fall through to another provider's path.
func TestSkillCatalog_PrefersAppNameDir(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	// Same skill in ~/.claude/skills and in wick's own dir.
	claudeSkill := filepath.Join(home, ".claude", "skills", "shared")
	if err := os.MkdirAll(claudeSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: shared\ndescription: d\n---\n"
	if err := os.WriteFile(filepath.Join(claudeSkill, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, home, "shared", "d")

	wantDir := filepath.Join(home, "."+appname.Resolve(), "skills", "shared")
	got := skillCatalog()
	if !strings.Contains(got, filepath.ToSlash(wantDir)) && !strings.Contains(got, wantDir) {
		t.Errorf("catalog should reference the app-name dir %q, got:\n%s", wantDir, got)
	}
}

// TestSkillMdPath_DevAppNameLabel pins the dir-label rule directly, without
// depending on what appname.Resolve() returns in the test process: a dev build
// labels its dir "wick-lab", and the catalog must still prefer it over another
// provider's copy. Matching the literal "wick" silently sent dev builds to a
// path outside wick's own tree.
func TestSkillMdPath_DevAppNameLabel(t *testing.T) {
	info := skillsync.SkillInfo{
		Name:  "shared",
		IsDir: true,
		InProviders: []skillsync.ProviderLocation{
			{Label: "claude", Dir: "/home/u/.claude/skills", Path: "/home/u/.claude/skills/shared"},
			{Label: "wick-lab", Dir: "/home/u/.wick-lab/skills", Path: "/home/u/.wick-lab/skills/shared"},
		},
	}
	got := skillMdPath(info, "wick-lab")
	want := "/home/u/.wick-lab/skills/shared/SKILL.md"
	if got != want {
		t.Errorf("skillMdPath = %q, want %q", got, want)
	}
}

// TestSkillCatalog_MarksBuiltin: a shipped skill must be labelled in the
// catalog. Without a UI, this label is how the agent knows it is reading
// official material it must not offer to edit — the file is rewritten on every
// start, so any edit it suggested would silently vanish.
func TestSkillCatalog_MarksBuiltin(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	if _, err := skillsync.SyncBuiltin(); err != nil {
		t.Fatalf("SyncBuiltin: %v", err)
	}
	writeSkill(t, home, "my-own", "a user skill")

	got := skillCatalog()
	if !strings.Contains(got, builtinTag) {
		t.Errorf("catalog does not mark any builtin skill with %q:\n%s", builtinTag, got)
	}
	// The user's own skill must NOT be tagged.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "my-own") && strings.Contains(line, builtinTag) {
			t.Errorf("user skill wrongly tagged builtin: %s", line)
		}
	}
}
