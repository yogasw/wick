// Package config holds the runtime-editable Agents config (General /
// Slack / Workspace structs reflected into the configs DB table) plus
// the on-disk Layout — the single source of truth for path math under
// BaseDir. Every other agents subpackage receives a Layout, never
// hand-rolls paths.
package config

import (
	"os"
	"path/filepath"
)

// Layout describes the on-disk folder layout rooted at BaseDir.
//
// Tests construct a Layout pointing at `t.TempDir()`; production code
// builds one from WorkspaceConfig.BaseDir (or the platform default).
type Layout struct {
	BaseDir string
}

func NewLayout(baseDir string) Layout { return Layout{BaseDir: baseDir} }

func (l Layout) PresetsDir() string  { return filepath.Join(l.BaseDir, "presets") }
func (l Layout) ProjectsDir() string { return filepath.Join(l.BaseDir, "projects") }
func (l Layout) SessionsDir() string { return filepath.Join(l.BaseDir, "sessions") }

func (l Layout) PresetDir(name string) string  { return filepath.Join(l.PresetsDir(), name) }
func (l Layout) PresetFile(name string) string { return filepath.Join(l.PresetDir(name), "agent.md") }

func (l Layout) ProjectDir(name string) string  { return filepath.Join(l.ProjectsDir(), name) }
func (l Layout) ProjectMeta(name string) string { return filepath.Join(l.ProjectDir(name), "meta.json") }
func (l Layout) ProjectWorkspace(name string) string {
	return filepath.Join(l.ProjectDir(name), "workspace")
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
func (l Layout) SessionWorkspace(id string) string {
	return filepath.Join(l.SessionDir(id), "workspace")
}

// EnsureLayout creates the three top-level folders if they don't exist.
// Idempotent — safe to call on every boot.
func (l Layout) EnsureLayout() error {
	for _, d := range []string{l.PresetsDir(), l.ProjectsDir(), l.SessionsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ResolveBaseDir returns the effective base directory: the configured
// value if set, else the platform default (~/.wick/agents).
func ResolveBaseDir(cfg WorkspaceConfig) string {
	if cfg.BaseDir != "" {
		return cfg.BaseDir
	}
	return defaultBaseDir()
}

// defaultBaseDir returns the platform default. Falls back to
// `./.wick/agents` when the home dir lookup fails so we never panic;
// operator can override via WorkspaceConfig.BaseDir.
func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".wick", "agents")
	}
	return filepath.Join(home, ".wick", "agents")
}
