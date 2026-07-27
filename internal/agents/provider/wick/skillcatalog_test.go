package wick

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/appname"
)

func setTestHome(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
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

func TestSkillCatalog_Empty(t *testing.T) {
	setTestHome(t, t.TempDir())
	if got := skillCatalog(); got != "" {
		t.Errorf("empty skills → want \"\", got %q", got)
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
