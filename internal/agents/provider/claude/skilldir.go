package claude

import (
	"os"
	"path/filepath"
)

// skillAddDirArgs grants the spawned agent read access to its skill
// bundle directory via --add-dir. Skills live under ~/.claude/skills/,
// outside the agent's workspace, so without this claude denies reads of
// a skill's sibling resource files (rules/, templates/, scripts/) — the
// agent silently falls back to whatever is inline in SKILL.md. Only an
// existing dir is added so a missing skills tree doesn't trip claude.
func skillAddDirArgs(home string, exists func(string) bool) []string {
	if home == "" {
		return nil
	}
	dir := filepath.Join(home, ".claude", "skills")
	if !exists(dir) {
		return nil
	}
	return []string{"--add-dir", dir}
}

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
