package codex

import (
	"os"
	"path/filepath"
)

// skilldir.go mirrors claude/skilldir.go for the codex CLI. skillsync
// copies every provider's skills into ~/.codex/skills, but under a
// workspace-write / read-only sandbox the codex process can't reach a
// skill's bundled resource files (they live outside cwd) unless the dir
// is explicitly trusted. claude does this with --add-dir; codex supports
// the same flag (see catalog.go), so wire it the same way. Without this
// the folders exist on disk but are unreadable to the running agent.

// homeDir resolves the user home dir. A package var so tests can point it
// at a temp dir and get deterministic --add-dir behaviour instead of
// depending on whether the developer/CI machine happens to have a real
// ~/.codex/skills (which would break the exact-argv assertions).
var homeDir = os.UserHomeDir

// skillAddDirArgs --add-dir's ~/.codex/skills (when present) so the agent
// can read a skill's bundled resource files outside its workspace.
func skillAddDirArgs(home string, exists func(string) bool) []string {
	if home == "" {
		return nil
	}
	dir := filepath.Join(home, ".codex", "skills")
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
