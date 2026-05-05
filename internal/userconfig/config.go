// Package userconfig persists per-machine user preferences for the
// system tray (auto-start toggles, default project, self-update state)
// in a single JSON file under the OS user-config directory.
//
// One installed binary = one config file. The directory is named after
// the running binary, so a user who installs the same app under two
// different names ("wick-manager", "client-tools") gets two separate
// configs without collision.
//
// Path per OS:
//
//	Windows : %APPDATA%\<binary>\config.json
//	macOS   : ~/Library/Application Support/<binary>/config.json
//	Linux   : ~/.config/<binary>/config.json
//
// Settings here are machine-wide, not per-project. Per-project state
// (e.g., wick app data) still lives in the project's wick.db.
package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the on-disk shape. Add fields with `json:"...,omitempty"`
// so older config files keep working when the binary upgrades.
type Config struct {
	// Tray auto-start toggles — applied at the next tray launch.
	AutoStartServer bool `json:"auto_start_server"`
	AutoStartWorker bool `json:"auto_start_worker"`

	// Self-update toggle.
	AutoUpdate bool `json:"auto_update"`

	// Cross-project pointer.
	DefaultProject string   `json:"default_project,omitempty"`
	RecentProjects []string `json:"recent_projects,omitempty"`

	// Update state — managed by the updater, not user-facing.
	StagedUpdatePath    string `json:"staged_update_path,omitempty"`
	StagedUpdateVersion string `json:"staged_update_version,omitempty"`
}

func defaults() Config {
	return Config{
		AutoStartServer: true,
		AutoStartWorker: false,
		AutoUpdate:      true,
	}
}

// Path returns the absolute config file path for the given project
// name. Empty name falls back to the running binary's basename.
func Path(name string) (string, error) {
	if name == "" {
		name = binaryName()
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name, "config.json"), nil
}

// Load reads the config file for the given project name. Missing file
// → defaults; parse errors surface to the caller.
func Load(name string) (Config, error) {
	path, err := Path(name)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults(), nil
		}
		return Config{}, err
	}
	cfg := defaults()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes the config atomically (write to temp, rename).
func Save(name string, cfg Config) error {
	path, err := Path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func binaryName() string {
	exe, err := os.Executable()
	if err != nil {
		return "wick"
	}
	name := filepath.Base(exe)
	if ext := filepath.Ext(name); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}
