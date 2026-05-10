// Package config holds the runtime-editable Agents config (General /
// Slack / Workspace structs reflected into the configs DB table) plus
// the on-disk Layout — the single source of truth for path math under
// the platform default data directory (~/.<app>/agents). Every other
// agents subpackage receives a Layout, never hand-rolls paths.
package config

import (
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/appname"
)

// Layout describes the on-disk folder layout rooted at BaseDir.
//
// Tests construct a Layout pointing at `t.TempDir()`; production code
// builds one from WorkspaceConfig.BaseDir (or the platform default).
type Layout struct {
	BaseDir string
}

func NewLayout(baseDir string) Layout { return Layout{BaseDir: baseDir} }

func (l Layout) PresetsDir() string    { return filepath.Join(l.BaseDir, "presets") }
func (l Layout) WorkspacesDir() string { return filepath.Join(l.BaseDir, "workspaces") }
func (l Layout) SessionsDir() string   { return filepath.Join(l.BaseDir, "sessions") }

func (l Layout) PresetDir(name string) string  { return filepath.Join(l.PresetsDir(), name) }
func (l Layout) PresetFile(name string) string { return filepath.Join(l.PresetDir(name), "agent.md") }

// WorkspaceDir is the metadata folder for one workspace
// (`workspaces/<name>/`). For managed workspaces this also contains
// the `files/` subfolder used as the agent cwd; custom workspaces
// store no files here, only meta.json.
func (l Layout) WorkspaceDir(name string) string {
	return filepath.Join(l.WorkspacesDir(), name)
}
func (l Layout) WorkspaceMeta(name string) string {
	return filepath.Join(l.WorkspaceDir(name), "meta.json")
}

// WorkspaceManagedPath is the cwd folder for a managed workspace
// (`workspaces/<name>/files/`). Use workspace.ResolvePath() instead
// of calling this directly — it transparently handles the custom
// path case.
func (l Layout) WorkspaceManagedPath(name string) string {
	return filepath.Join(l.WorkspaceDir(name), "files")
}

func (l Layout) SessionDir(id string) string  { return filepath.Join(l.SessionsDir(), id) }
func (l Layout) SessionMeta(id string) string { return filepath.Join(l.SessionDir(id), "meta.json") }
func (l Layout) SessionAgents(id string) string {
	return filepath.Join(l.SessionDir(id), "agents.json")
}
func (l Layout) SessionAgentMD(id string) string {
	return filepath.Join(l.SessionDir(id), "agent.md")
}
func (l Layout) SessionConversation(id string) string {
	return filepath.Join(l.SessionDir(id), "conversation.jsonl")
}
func (l Layout) SessionCommands(id string) string {
	return filepath.Join(l.SessionDir(id), "commands.jsonl")
}
func (l Layout) SessionRaw(id string) string {
	return filepath.Join(l.SessionDir(id), "raw.jsonl")
}

// EnsureLayout creates the three top-level folders if they don't exist.
// Idempotent — safe to call on every boot.
func (l Layout) EnsureLayout() error {
	for _, d := range []string{l.PresetsDir(), l.WorkspacesDir(), l.SessionsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ResolveBaseDir returns the platform default base directory (~/.wick/agents).
// The cfg parameter is kept for call-site compatibility.
func ResolveBaseDir(_ WorkspaceConfig) string {
	return defaultBaseDir()
}

// defaultBaseDir returns the platform default `~/.<app>/agents`,
// falling back to `./.<app>/agents` when home dir lookup fails so we
// never panic. `<app>` comes from appname.Resolve() so every wick
// app's agents tree lives under the same per-app namespace as its DB.
func defaultBaseDir() string {
	app := appname.Resolve()
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "."+app, "agents")
	}
	return filepath.Join(home, "."+app, "agents")
}
