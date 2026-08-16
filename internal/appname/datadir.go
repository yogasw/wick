package appname

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DataDirEnv is the env var that overrides the root data directory.
//
// Unlike APP_NAME (a display label, deliberately kept out of path math —
// see the package doc), this one IS a path and is the only supported way
// to relocate the tree for a downloaded binary: `BuildAppName` is baked in
// at compile time and wick.yml is absent next to a release binary, so
// without this env a user has no way to move their data off `~/.<app>/`.
const DataDirEnv = "WICK_DATA_DIR"

var (
	dataDirOnce sync.Once
	dataDir     string
)

// DataDir returns the root directory every wick artefact lives under —
// the DB, config.json, logs, skills, plugins, and the whole agents tree.
// Sub-trees are derived from it, never rebuilt from the home dir, so a
// single override moves all of them together.
//
// Resolution order (first non-empty wins):
//
//  1. $WICK_DATA_DIR — absolute path, or `~/...` which expands to the
//     home dir. A relative path is made absolute against the cwd.
//  2. ~/.<app>/     — the platform default, `<app>` from Resolve().
//  3. ./.<app>/     — only when the home dir lookup fails, so callers
//     never have to handle an error or a panic.
//
// Cached for the process lifetime: the inputs are fixed at start-up, and
// a path that changed mid-run would split one process's data across two
// trees. Tests call ResetDataDirForTest to re-resolve.
func DataDir() string {
	dataDirOnce.Do(func() { dataDir = resolveDataDir() })
	return dataDir
}

// ResetDataDirForTest clears the cached DataDir so the next call
// re-reads $WICK_DATA_DIR. Test-only; production inputs never change
// at runtime.
func ResetDataDirForTest() {
	mu.Lock()
	defer mu.Unlock()
	dataDirOnce = sync.Once{}
	dataDir = ""
}

func resolveDataDir() string {
	if d := strings.TrimSpace(os.Getenv(DataDirEnv)); d != "" {
		return normalizeDataDir(d)
	}
	hidden := "." + Resolve()
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", hidden)
	}
	return filepath.Join(home, hidden)
}

// normalizeDataDir expands a leading `~` and makes the result absolute.
// A relative override is resolved against the cwd, which matters because
// wick chdirs to the project root on some entry points (MCP stdio) — an
// absolute path taken once here can't drift afterwards.
func normalizeDataDir(d string) string {
	if d == "~" || strings.HasPrefix(d, "~/") || strings.HasPrefix(d, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			d = filepath.Join(home, strings.TrimPrefix(d[1:], string(filepath.Separator)))
		}
	}
	if abs, err := filepath.Abs(d); err == nil {
		return abs
	}
	return filepath.Clean(d)
}

// DataDirOverride returns the normalized $WICK_DATA_DIR, or "" when the
// env is unset. Callers that keep their own default (userconfig.Dir,
// whose fallback is keyed on a caller-supplied app name rather than on
// Resolve()) use this to honour the override without giving up that
// default. Everything else should just call DataDir.
func DataDirOverride() string {
	if d := strings.TrimSpace(os.Getenv(DataDirEnv)); d != "" {
		return normalizeDataDir(d)
	}
	return ""
}

// AgentsDir is the agents sub-tree (`<DataDir>/agents`): sessions,
// projects, presets, workflows, the gate dir, and the control sockets.
func AgentsDir() string { return filepath.Join(DataDir(), "agents") }

// SkillsDir is wick's own skills folder (`<DataDir>/skills`) — the sync
// target that sits alongside ~/.claude/skills and friends.
func SkillsDir() string { return filepath.Join(DataDir(), "skills") }

// LogsDir holds the per-day log files (`<DataDir>/logs`) written by the
// server, worker, app, and gate.
func LogsDir() string { return filepath.Join(DataDir(), "logs") }
