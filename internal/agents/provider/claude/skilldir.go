package claude

import (
	"os"
	"path/filepath"
)

// skillAddDirArgs --add-dir's ~/.claude/skills (when present) so the
// agent can read a skill's bundled resource files outside its workspace.
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
