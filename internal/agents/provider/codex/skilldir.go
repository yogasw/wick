package codex

import (
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/skillsync"
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

// codexSkillsDir returns the skills dir the codex CLI itself loads from.
//
// $CODEX_HOME relocates codex's whole config tree, skills included, so a
// machine that sets it keeps its skills somewhere other than ~/.codex. The
// hardcoded fallback covers the common case where it is unset.
//
// codexHome is the spawn's OWN CODEX_HOME, lifted from the instance env —
// not wick's. They differ whenever a provider instance carries
// CODEX_HOME in its Env, which is how one host runs several codex
// accounts side by side. Reading the process env here would trust
// whichever home wick itself was started with and --add-dir the wrong
// account's skills, so the instance value wins and the process env is
// only the fallback for instances that set nothing.
func codexSkillsDir(home, codexHome string) string {
	if codexHome == "" {
		codexHome = os.Getenv("CODEX_HOME")
	}
	if codexHome != "" {
		return filepath.Join(codexHome, "skills")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "skills")
}

// skillAddDirArgs --add-dir's every skills dir the agent may need to read from,
// so it can open a skill's bundled resource files outside its workspace.
//
// Two dirs, for two reasons. codex's own dir holds the skills skillsync mirrors
// there, and the sandbox hides them without this. Wick's own dir holds the
// skills that ship inside the binary, which are deliberately NOT mirrored (see
// skillsync/builtin.go) — the system prompt points at them by absolute path, so
// without the trust here the agent would be told to read files it is not
// allowed to open.
func skillAddDirArgs(home, codexHome string, exists func(string) bool) []string {
	var args []string
	for _, dir := range []string{codexSkillsDir(home, codexHome), skillsync.BuiltinDir()} {
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
