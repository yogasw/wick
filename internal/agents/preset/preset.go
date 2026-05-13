// Package preset manages reusable agent templates stored at
// `<BaseDir>/presets/<name>/agent.md`. The whole preset body is just
// the agent.md file — no separate metadata.
//
// Sessions snapshot the file contents into the session folder when an
// agent is created (so later edits to the preset don't rewrite live
// agents).
package preset

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// DefaultName is the built-in preset every fresh install gets. It is
// the fallback when a session is created without a workspace (or when
// the workspace has no DefaultPreset), and it cannot be deleted —
// removing it would leave sessions with no preset to load.
const DefaultName = "default"

// Preset is the in-memory shape returned by Load.
type Preset struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// Create writes presets/<name>/agent.md. Refuses to overwrite an
// existing preset to avoid clobbering an in-use template; callers that
// want to update should use Update.
func Create(layout config.Layout, name, body string) error {
	if err := storage.ValidatePresetName(name); err != nil {
		return err
	}
	dir := layout.PresetDir(name)
	if storage.PathExists(dir) {
		return fmt.Errorf("preset %q already exists", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(layout.PresetFile(name), []byte(body), 0o644)
}

// Update rewrites the agent.md of an existing preset.
func Update(layout config.Layout, name, body string) error {
	if err := storage.ValidatePresetName(name); err != nil {
		return err
	}
	if !storage.PathExists(layout.PresetDir(name)) {
		return fmt.Errorf("preset %q not found", name)
	}
	return os.WriteFile(layout.PresetFile(name), []byte(body), 0o644)
}

// Load reads presets/<name>/agent.md.
func Load(layout config.Layout, name string) (Preset, error) {
	if err := storage.ValidatePresetName(name); err != nil {
		return Preset{}, err
	}
	body, err := os.ReadFile(layout.PresetFile(name))
	if err != nil {
		return Preset{}, err
	}
	return Preset{Name: name, Body: string(body)}, nil
}

// List returns every preset folder name, sorted.
func List(layout config.Layout) ([]string, error) {
	return storage.ScanDirNames(layout.PresetsDir())
}

// Delete removes the preset folder. Existing sessions that
// snapshotted from this preset are unaffected (they keep their own
// copy in sessions/<id>/agent.md).
func Delete(layout config.Layout, name string) error {
	if err := storage.ValidatePresetName(name); err != nil {
		return err
	}
	if name == DefaultName {
		return fmt.Errorf("preset %q is built-in and cannot be deleted", name)
	}
	return os.RemoveAll(layout.PresetDir(name))
}

// EnsureDefault writes presets/default/agent.md with a minimal body
// if no default preset exists yet. Called from Bootstrap so fresh
// installs aren't missing the preset that projects/sessions fall back
// to.
func EnsureDefault(layout config.Layout) error {
	path := layout.PresetFile(DefaultName)
	if storage.PathExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := "# Default agent\n\nYou are an AI assistant working inside a wick session worktree. Stay focused on the user's task.\n"
	return os.WriteFile(path, []byte(body), 0o644)
}
