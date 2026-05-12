// Package userconfig persists per-machine user preferences for the
// system tray (auto-start toggles, default project, self-update state)
// in a single JSON file under a hidden app directory in the user's home.
//
// One installed binary = one config file. The directory is named after
// the running binary, so a user who installs the same app under two
// different names ("wick-manager", "client-tools") gets two separate
// configs without collision.
//
// Path:
//
//	~/.<binary>/config.json
//
// Settings here are machine-wide, not per-project. Per-project state
// (e.g., wick app data) still lives in the project's wick.db when
// launched from a project directory.
package userconfig

import (
	"encoding/json"
	"io"
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
	// otherwise ~/.<appName>/wick.db.
	// Set this manually in config.json if you need a custom location.
	DatabasePath string `json:"database_path,omitempty"`

	// Update state — managed by the updater, not user-facing.
	StagedUpdatePath    string `json:"staged_update_path,omitempty"`
	StagedUpdateVersion string `json:"staged_update_version,omitempty"`

	// Providers holds per-AI-provider overrides for the agents module
	// (claude / codex / gemini). Each provider keeps its own binary
	// path override + extra args. Empty / nil = full auto-detect via
	// PATH lookup.
	Providers ProvidersConfig `json:"providers,omitempty"`

	// ProviderStatuses caches the last-known Probe result per
	// instance, keyed `<type>/<name>`. Survives restart so the
	// Providers page renders instantly instead of waiting on cold
	// `--version` spawns. Refresh policy is owned by the agents
	// module — this layer is a dumb store. Empty / nil = no cache.
	ProviderStatuses map[string]ProviderStatus `json:"provider_statuses,omitempty"`
}

// ProviderStatus is the persisted shape of a Probe result.
//
// Hooks holds per-event capability info (currently just "PreToolUse"
// for the command gate; future events like "SessionStart" or
// "UserPromptSubmit" land as additional map keys without struct churn).
// Persisting it here means the Providers page renders the gate-toggle
// state from disk without re-spawning the provider on every render —
// same TTL strategy as the version probe. Re-probe only fires when
// Version changes or the user clicks Rescan.
type ProviderStatus struct {
	Path       string `json:"path"`
	PathFound  bool   `json:"path_found"`
	Version    string `json:"version,omitempty"`
	VersionErr string `json:"version_err,omitempty"`
	ScannedAt  string `json:"scanned_at,omitempty"`
	VersionAt  string `json:"version_at,omitempty"`

	// Hooks captures the runtime capability check per hook event name.
	// Keys are provider-agnostic event names ("PreToolUse",
	// "SessionStart", ...). Empty map = never probed, UI surfaces
	// "click Test to verify".
	Hooks map[string]HookCapability `json:"hooks,omitempty"`
}

// HookCapability is the persisted snapshot of one hook-event probe.
// Mirrors capability.Capability — kept here as a separate struct so
// the userconfig package stays self-contained (no import of
// internal/agents/capability, which would invert the dependency).
type HookCapability struct {
	Supported bool   `json:"supported,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
	ProbedAt  string `json:"probed_at,omitempty"`
	Error     string `json:"error,omitempty"`
	Scope     string `json:"scope,omitempty"` // "bash+edit+mcp" | "shell-only" | "untested"
}

// ProvidersConfig groups per-provider-type instance lists. One type
// (e.g. "claude") can hold multiple named instances so the user can
// run two different binaries / credential sets in parallel — typical
// case is a "work" claude on a corporate PAT next to a "personal"
// claude on a different PAT.
//
// Bootstrap rule: on first boot the agents bootstrap auto-seeds one
// instance per supported type whose Name equals the type itself
// (`claude`, `codex`, `gemini`) with BinaryPath empty so LookPath
// resolves the canonical binary on PATH. Adding more instances is
// purely user-driven via the Providers page.
type ProvidersConfig struct {
	Claude []ProviderInstance `json:"claude,omitempty"`
	Codex  []ProviderInstance `json:"codex,omitempty"`
	Gemini []ProviderInstance `json:"gemini,omitempty"`
}

// ProviderInstance is one named configuration of a provider type.
// Name must be unique within a single type ("claude" can have one
// "work" + one "personal" but two "work" entries collide).
//
// BinaryPath: absolute path to the CLI binary. Empty = LookPath the
// canonical type name on PATH.
//
// Disabled: hide from new-session pickers and refuse to spawn. Useful
// when an instance is detected but known broken.
//
// ExtraArgs: extra CLI flags appended after the canonical headless
// flags, before --resume. Forwarded to the provider's Spawner.
//
// Env: extra `KEY=VALUE` pairs merged into the subprocess env on
// every spawn. The primary use case is per-instance credentials
// (different ANTHROPIC_API_KEY between work and personal claude)
// without leaking those into the user's global shell env.
type ProviderInstance struct {
	Name       string   `json:"name"`
	BinaryPath string   `json:"binary_path,omitempty"`
	Disabled   bool     `json:"disabled,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
	Env        []string `json:"env,omitempty"`

	// Hooks captures the user's intent per hook event: "do you want
	// wick to route this hook through the gate?". Keys are event
	// names (PreToolUse for the command gate today; future events
	// like SessionStart land as additional keys without schema
	// churn). Absent / Enabled=false means the provider's own
	// permission flow applies — no hook config gets installed on
	// spawn.
	Hooks map[string]HookInstanceConfig `json:"hooks,omitempty"`

	// Storage configures credential/config file syncing for this
	// instance. nil = sync disabled.
	Storage *StorageConfig `json:"storage,omitempty"`
}

// StorageConfig defines how a provider instance syncs its credential
// files to the DB.
//
// Mode "folder" syncs all files under SyncPath recursively.
// Mode "single" syncs only the file at SyncPath.
// IntervalSeconds controls how often the background ticker runs; 0 disables
// background sync (startup-only).
type StorageConfig struct {
	Mode            string `json:"mode"`             // "folder" | "single"
	SyncPath        string `json:"sync_path"`        // abs path to file or folder
	IntervalSeconds int    `json:"interval_seconds"` // 0 = startup only
}

// HookInstanceConfig is the user's stored intent for one hook event
// on one provider instance. Kept as a struct (not just a bool) so we
// can grow per-event knobs (mode, allowlist, per-tool override)
// without another schema migration.
type HookInstanceConfig struct {
	Enabled bool `json:"enabled,omitempty"`
}

func defaults() Config {
	return Config{
		AutoStartServer: false,
		AutoStartWorker: false,
		AutoUpdate:      true,
	}
}

// Dir returns the absolute per-app data directory. Empty name falls
// back to the running binary's basename.
func Dir(name string) (string, error) {
	if name == "" {
		name = binaryName()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, hiddenName(name)), nil
}

// Path returns the absolute config file path for the given project
// name. Empty name falls back to the running binary's basename.
func Path(name string) (string, error) {
	dir, err := Dir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
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
			if legacy, lerr := legacyPath(name); lerr == nil {
				if data, rerr := os.ReadFile(legacy); rerr == nil {
					cfg := defaults()
					if err := json.Unmarshal(data, &cfg); err != nil {
						return Config{}, err
					}
					return cfg, nil
				}
			}
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
//  4. ~/.<appName>/wick.db (standalone / downloaded binary)
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
	dir, err := Dir(appName)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	dbPath := filepath.Join(dir, "wick.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if legacy, lerr := legacyDBPath(appName); lerr == nil {
			_ = copyFile(legacy, dbPath)
		}
	}
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

// hiddenName turns an app name into a path-safe hidden directory name.
// Slugifies: lowercase, spaces → "-", strips chars that break Windows
// paths (< > : " / \ | ? *) and leading dots. "My App" → ".my-app".
func hiddenName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.TrimLeft(name, ".")
	name = strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r == ' ' || r == '\t':
			b.WriteByte('-')
		case r == '<' || r == '>' || r == ':' || r == '"' || r == '/' || r == '\\' || r == '|' || r == '?' || r == '*':
			// drop
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), "-.")
	if name == "" {
		name = "wick"
	}
	return "." + name
}

func legacyPath(name string) (string, error) {
	if name == "" {
		name = binaryName()
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name, "config.json"), nil
}

func legacyDBPath(appName string) (string, error) {
	if appName == "" {
		appName = binaryName()
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName, "wick.db"), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
