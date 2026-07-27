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

	// Wick is the built-in in-process provider. Single-instance by
	// design: the list never holds more than the one "wick" entry —
	// multiplicity lives in that instance's WickModels, not in extra
	// instances. Enforced by provider.Save/Rename, kept as a slice so
	// the ProvidersConfig shape stays uniform across types.
	Wick []ProviderInstance `json:"wick,omitempty"`
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
// UserModelEntry is one curated model on a provider instance: an alias plus
// an optional description, both hand-editable. UnmarshalJSON accepts either
// the current object form ({"id":"opus","desc":"…"}) or the legacy plain
// string form ("opus") so configs written before the description column
// migrate transparently on first load.
type UserModelEntry struct {
	ID   string `json:"id"`
	Desc string `json:"desc,omitempty"`
}

func (m *UserModelEntry) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) > 0 && b[0] == '"' {
		// Legacy form: a bare string alias.
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		m.ID = s
		m.Desc = ""
		return nil
	}
	// Current form: an object. Use an alias type to avoid recursing into
	// this UnmarshalJSON.
	type raw UserModelEntry
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*m = UserModelEntry(r)
	return nil
}

type ProviderInstance struct {
	Name       string   `json:"name"`
	BinaryPath string   `json:"binary_path,omitempty"`
	Disabled   bool     `json:"disabled,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
	Env        []string `json:"env,omitempty"`

	// ModelSelect turns on the model picker for this CLI instance
	// (claude/codex/gemini). When on, the composer offers Models (or the
	// per-type seed defaults when Models is empty) and the chosen model is
	// passed to the CLI via --model on spawn. Off = the CLI's own default
	// model, no picker (unchanged behaviour). Ignored by wick (which has
	// its own WickModels).
	ModelSelect bool `json:"model_select,omitempty"`
	// Models is the user-curated list of models offered when ModelSelect is
	// on: each an alias plus an optional description. Empty → the per-type
	// seed defaults are used. CLIs can't enumerate their own models, so this
	// is seeded + user-editable rather than discovered. UserModelEntry
	// unmarshals both the current object form and the legacy plain-string
	// form so existing configs migrate transparently.
	Models []UserModelEntry `json:"models,omitempty"`

	// Hooks captures the user's intent per hook event: "do you want
	// wick to route this hook through the gate?". Keys are event
	// names (PreToolUse for the command gate today; future events
	// like SessionStart land as additional keys without schema
	// churn). Absent / Enabled=false means the provider's own
	// permission flow applies — no hook config gets installed on
	// spawn.
	Hooks map[string]HookInstanceConfig `json:"hooks,omitempty"`

	// SandboxMode sets --sandbox for codex spawns: "read-only",
	// "workspace-write", or "danger-full-access". Empty = danger-full-access.
	// Only meaningful for codex instances; ignored by claude/gemini.
	SandboxMode string `json:"sandbox_mode,omitempty"`

	// MaxConcurrent caps how many parallel spawns this instance may
	// have running at once. 0 = unlimited (follows the global pool cap).
	MaxConcurrent int `json:"max_concurrent,omitempty"`

	// SendMode overrides how this instance delivers a user message to its
	// CLI. Empty = the provider type's default (claude → "append", codex →
	// "queue"). Values: "append" (persistent stdin, one process; the CLI
	// queues input itself), "queue" (one-shot per turn, mid-turn sends
	// wait then run in order — every message processed, none lost), "spawn"
	// (one-shot, every send a fresh parallel process — no queue, contexts
	// independent). See provider.SendMode.
	SendMode string `json:"send_mode,omitempty"`

	// Storage configures credential/config file syncing for this
	// instance. nil = sync disabled.
	Storage *StorageConfig `json:"storage,omitempty"`

	// UseAIRouter routes this instance's CLI through an embedded AI router
	// proxy (base URL <wick-origin>/airouter/<id>/v1) instead of the
	// provider's own upstream. Only meaningful for claude/codex.
	//
	// JSON key kept as "use_9router" for backward compatibility with configs
	// written before the AI-router generalisation — existing 9router-routed
	// instances load unchanged and default to the 9router backend.
	UseAIRouter bool `json:"use_9router,omitempty"`

	// AIRouterProvider selects which registered router this instance routes
	// through ("9router", "omniroute", …). Empty = default (9router).
	AIRouterProvider string `json:"airouter_provider,omitempty"`

	// AIRouterModels maps a per-provider model slot key (e.g. "opus",
	// "sonnet", "haiku" for claude; "model", "subagent" for codex) to the
	// concrete model id chosen for it. Which slots exist is defined by the
	// selected router's SpawnHook. JSON key kept for back-compat.
	AIRouterModels map[string]string `json:"router9_models,omitempty"`

	// AIRouterAPIKey is a custom router API key (encrypted at rest via the
	// secret layer). Empty falls back to the router's default credential.
	// JSON key kept for back-compat.
	AIRouterAPIKey string `json:"router9_api_key,omitempty"`

	// AIRouterRawConfig is free-form extra config appended to the router spawn
	// (codex `-c` overrides / claude env), one entry per line.
	AIRouterRawConfig string `json:"airouter_raw_config,omitempty"`

	// WickModels is the custom-model registry for the built-in wick
	// provider — one entry per registered model (Gemini / OpenAI /
	// Anthropic / OpenRouter / other). Only meaningful for the wick
	// instance; ignored by claude/codex/gemini.
	WickModels []WickModel `json:"wick_models,omitempty"`

	// WickConfig holds the wick provider's instance-level settings
	// (tools, context budget, generation defaults). nil = defaults.
	WickConfig *WickConfig `json:"wick_config,omitempty"`
}

// WickModel is one registered custom model on the wick provider.
// See internal/planning/in-progress/wick-provider/plan.md.
type WickModel struct {
	// ID is the stable identifier ("m_" + random) referenced by the
	// Default flag and by sessions that pinned a specific model.
	ID string `json:"id"`
	// Kind is the vendor family: google | openai | anthropic |
	// openrouter | other.
	Kind string `json:"kind"`
	// Label is the display name; empty = show Model.
	Label string `json:"label,omitempty"`
	// Model is the vendor model id (gemini-flash-latest, gpt-5.2, …).
	Model string `json:"model"`
	// APIKey is encrypted at rest (wick_cenc_ token).
	APIKey string `json:"api_key,omitempty"`
	// BaseURL overrides the vendor endpoint; required for kind=other.
	BaseURL string `json:"base_url,omitempty"`
	// APIFormat picks the wire protocol: gemini | openai_chat |
	// openai_responses | anthropic_messages. Empty = derived from Kind.
	APIFormat string `json:"api_format,omitempty"`
	// MaxOutputTokens caps the response size. 0 = vendor default.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	// Default marks the model sessions use unless they pin another.
	// Exactly one entry per instance holds true (enforced on save).
	Default bool `json:"default,omitempty"`
	// Disabled hides the model from the composer's model picker and
	// from default-selection, without deleting its (possibly hard-won)
	// config. Stays visible, greyed out, in the Models table.
	Disabled bool `json:"disabled,omitempty"`
	// GenConfig holds per-model generation overrides; nil = the
	// instance-level WickConfig.GenConfig applies.
	GenConfig *WickGenConfig `json:"gen_config,omitempty"`
	// RawConfig is per-model raw ADK config (JSON), merged last.
	RawConfig string `json:"raw_config,omitempty"`
	// DiscoveryFilter, when set, makes this a LIVE model set rather than a
	// single pinned model: at picker time the vendor's model list is fetched
	// and filtered by this query (tiny grammar: space-separated terms;
	// `term` = contains, `-term`/`!term` = exclude — matched over id+label).
	// Model may be empty for a live set. Lets one entry stand in for many
	// models without registering each by hand.
	DiscoveryFilter string `json:"discovery_filter,omitempty"`
	// DefaultVendorModel is the sticky default vendor model id WITHIN a live
	// set (only meaningful when DiscoveryFilter is set). When this live set is
	// picked without an explicit "@vendor" override, the spawn uses this id if
	// it's still present in the freshly-fetched list; if it has vanished, the
	// picker/spawn auto-fall back to the top of the filtered list. Empty = no
	// pin, top-of-list is the effective default.
	DefaultVendorModel string `json:"default_vendor_model,omitempty"`
}

// WickConfig is the instance-level settings block for the wick
// provider ("Provider settings" card in the UI).
type WickConfig struct {
	// ShellToolDisabled turns the bash/cmd tool off. Stored inverted
	// so the zero value keeps the shell tool enabled (default on).
	ShellToolDisabled bool `json:"shell_tool_disabled,omitempty"`
	// Connectors limits which connector instances become tools.
	// Empty = all ready connectors.
	Connectors []string `json:"connectors,omitempty"`
	// MaxContextTokens is the history-replay budget. 0 = model default.
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	// MaxTurns caps the agentic loop per user turn. 0 = unlimited.
	MaxTurns int `json:"max_turns,omitempty"`
	// MaxConsecErrors cuts a turn after N consecutive all-error tool
	// rounds (a success resets the counter). 0 = default (20).
	MaxConsecErrors int `json:"max_consec_errors,omitempty"`
	// MaxTurnMinutes is the wall-clock ceiling for one turn. 0 = default (60).
	MaxTurnMinutes int `json:"max_turn_minutes,omitempty"`
	// MaxModelRetries is the total attempts for a failing model call (incl. the
	// first). 0 = default (3). 1 disables retries.
	MaxModelRetries int `json:"max_model_retries,omitempty"`
	// ModelCallTimeoutSec bounds one model-call attempt. 0 = default (120s).
	ModelCallTimeoutSec int `json:"model_call_timeout_sec,omitempty"`
	// GenConfig is the default generation config for models without
	// their own override.
	GenConfig *WickGenConfig `json:"gen_config,omitempty"`
	// RawConfig is raw ADK config (JSON) merged into the runner config
	// — the escape hatch that keeps every adk-go knob reachable before
	// a structured field exists for it.
	RawConfig string `json:"raw_config,omitempty"`
}

// WickGenConfig mirrors the common genai.GenerateContentConfig knobs.
// Pointer fields distinguish "not set" from zero. The long tail of
// options rides WickConfig.RawConfig / WickModel.RawConfig.
type WickGenConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	ThinkingBudget  *int     `json:"thinking_budget,omitempty"` // tokens; 0 = off, nil = model default
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
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
		// Opt-in: the user must enable auto-update explicitly (tray
		// Preferences or the admin System page). Background update checks
		// stay off until then.
		AutoUpdate: false,
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
	return filepath.Join(home, HiddenName(name)), nil
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
//
// The temp file gets a unique name per write (via os.CreateTemp) rather
// than a fixed "config.json.tmp". Concurrent writers to the same config
// dir — e.g. a foreground Save racing the background rescan goroutine
// that Save itself spawns — would otherwise clobber each other's shared
// temp file, and one rename would fail with "no such file or directory"
// once the other consumed the temp first.
func Save(name string, cfg Config) error {
	path, err := Path(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "config-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: if anything below fails the temp must not linger.
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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

// HiddenName turns an app name into a path-safe hidden directory name.
// Slugifies: lowercase, spaces → "-", strips chars that break Windows
// paths (< > : " / \ | ? *) and leading dots. "My App" → ".my-app".
// Exported so other packages derive the same ~/.<appName> dir from a resolved
// app name (e.g. the connector-plugin dir, which must match wick.db's tree).
func HiddenName(name string) string {
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
