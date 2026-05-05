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
	"strconv"
	"strings"
)

// Config is the on-disk shape. Add fields with `json:"...,omitempty"`
// so older config files keep working when the binary upgrades.
type Config struct {
	// AutoStartApp registers the binary with the OS so it launches at
	// user login (Windows: Run registry, macOS: LaunchAgent plist,
	// Linux: XDG autostart .desktop). Toggle from Preferences ▶ Auto-start app.
	AutoStartApp bool `json:"auto_start_app"`

	// Tray auto-start toggles — applied at the next tray launch.
	AutoStartServer bool `json:"auto_start_server"`
	AutoStartWorker bool `json:"auto_start_worker"`

	// Self-update toggle.
	AutoUpdate bool `json:"auto_update"`

	// Port overrides the HTTP listen port. 0 = use env PORT or default 9425.
	// Set this in config.json to pin a custom port without touching .env.
	Port int `json:"port,omitempty"`

	// LogRetentionDays controls how many days of per-day log files are
	// kept. 0 = use built-in default (7). Set in config.json to override.
	LogRetentionDays int `json:"log_retention_days,omitempty"`

	// DatabasePath overrides the SQLite DB location. Empty = auto-detect.
	// Auto-detect: binary dir has wick.yml → <binary_dir>/wick.db,
	// otherwise %APPDATA%/<appName>/wick.db (Windows) or equivalent.
	// Set this manually in config.json if you need a custom location.
	DatabasePath string `json:"database_path,omitempty"`

	// Update state — managed by the updater, not user-facing.
	StagedUpdatePath    string `json:"staged_update_path,omitempty"`
	StagedUpdateVersion string `json:"staged_update_version,omitempty"`
}

func defaults() Config {
	return Config{
		AutoStartServer: false,
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

// ResolveDBPath determines the SQLite DB path and sets DATABASE_URL so
// config.Load() picks it up wherever it is called next.
//
// Resolution order (first non-empty wins, never overwrites a higher priority):
//  1. DATABASE_URL env already set (explicit env / CI override) → untouched
//  2. cfg.DatabasePath set (user edited database_path in config.json)
//  3. <binary_dir>/wick.db when wick.yml exists next to the binary (project mode)
//  4. <UserConfigDir>/<appName>/wick.db (standalone / downloaded binary)
func ResolveDBPath(appName, customPath string) {
	if os.Getenv("DATABASE_URL") != "" {
		return
	}
	if customPath != "" {
		os.Setenv("DATABASE_URL", customPath)
		return
	}
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		binDir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(binDir, "wick.yml")); err == nil {
			dbPath := filepath.Join(binDir, "wick.db")
			os.Setenv("DATABASE_URL", dbPath)
			return
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dbPath := filepath.Join(base, appName, "wick.db")
	os.Setenv("DATABASE_URL", dbPath)
}

// ResolvePort sets the PORT env from cfg.Port so config.Load() picks
// it up wherever it is called next.
//
// Resolution order (first non-empty wins, never overwrites a higher priority):
//  1. PORT env already set (explicit env / CI override) → untouched
//  2. customPort > 0 (user edited port in config.json)
//  3. fall through → env.go envDefault picks the built-in default (9425)
func ResolvePort(customPort int) {
	if os.Getenv("PORT") != "" {
		return
	}
	if customPort > 0 {
		os.Setenv("PORT", strconv.Itoa(customPort))
	}
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
