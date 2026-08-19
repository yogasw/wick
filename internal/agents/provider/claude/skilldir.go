package claude

import (
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

// claudeSkillsDir returns the skills dir the claude CLI itself loads from.
//
// $CLAUDE_CONFIG_DIR relocates claude's whole config tree, skills included, so
// a machine that sets it keeps its skills somewhere other than ~/.claude. The
// hardcoded fallback covers the common case where it is unset.
func claudeSkillsDir(home string) string {
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		return filepath.Join(cfg, "skills")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "skills")
}

// skillAddDirArgs --add-dir's every skills dir the agent may need to read from,
// so it can open a skill's bundled resource files outside its workspace.
//
// Two dirs, for two reasons. claude's own dir holds the skills skillsync
// mirrors there, and the sandbox hides them without this. Wick's own dir holds
// the skills that ship inside the binary, which are deliberately NOT mirrored
// (see skillsync/builtin.go) — the system prompt points at them by absolute
// path, so without the trust here the agent would be told to read files it is
// not allowed to open.
func skillAddDirArgs(home string, exists func(string) bool) []string {
	var args []string
	for _, dir := range []string{claudeSkillsDir(home), skillsync.BuiltinDir()} {
		if dir == "" || !exists(dir) {
			continue
		}
		args = append(args, "--add-dir", dir)
	}
	return args
}

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
