package skillsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/appname"
)

// TestKnownDirsIncludesWick: the wick skills dir is always present (ensure-
// created) so wick is a first-class skill provider even before any skill is
// added — the fix that made wick appear in the skills UI / `/` menu.
func TestKnownDirsIncludesWick(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	dirs := KnownDirs()
	wickDir := filepath.Join(home, "."+appname.Resolve(), "skills")

	// The wick dir must be created on disk...
	if fi, err := os.Stat(wickDir); err != nil || !fi.IsDir() {
		t.Fatalf("wick skills dir not created: %v", err)
	}
	// ...and returned by KnownDirs.
	found := false
	for _, d := range dirs {
		if d == wickDir {
			found = true
		}
	}
	if !found {
		t.Errorf("KnownDirs missing wick dir %q; got %v", wickDir, dirs)
	}
	// Its label resolves to the app name (wick / wick-lab / …).
	if got := DirLabel(wickDir); got != appname.Resolve() {
		t.Errorf("DirLabel(%q) = %q, want %q", wickDir, got, appname.Resolve())
	}
}

// TestDirLabelProviders: the generic label parse yields the expected short
// names for each provider dir.
func TestDirLabelProviders(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cases := map[string]string{
		filepath.Join(home, ".claude", "skills"): "claude",
		filepath.Join(home, ".codex", "skills"):  "codex",
		filepath.Join(home, ".gemini", "skills"): "gemini",
		filepath.Join(home, ".agents", "skills"): "agents",
	}
	for dir, want := range cases {
		if got := DirLabel(dir); got != want {
			t.Errorf("DirLabel(%q) = %q, want %q", dir, got, want)
		}
	}
}
