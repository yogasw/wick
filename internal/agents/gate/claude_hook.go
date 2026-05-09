package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Spec describes one gate setup for one spawn — the bundle of paths
// + env that the gate binary needs to make a decision and log it.
//
// Generated per Spawn by pool/factory.go; written to a file under
// the session's gate-temp dir so the path can be passed to claude
// via `--settings <path>` and to the gate binary via env vars.
type Spec struct {
	SessionID string        `json:"session_id"`
	AgentName string        `json:"agent_name"`
	Layout    SpecLayout    `json:"layout"`
	Rules     []CommandRule `json:"rules"`

	// SocketPath is the Unix socket the gate binary dials when a
	// command is not auto-allowed. Empty = no interactive approval
	// (whitelist-only mode, fail-safe block on unlisted commands).
	SocketPath string `json:"socket_path,omitempty"`

	// AutoApproved holds matchKey hashes the user already chose
	// "Always allow" for. The gate binary checks this list before
	// dialing the socket so always-approved commands take a zero-
	// latency fast path identical to whitelisted ones. Rewritten by
	// the daemon when the user toggles always-allow / revoke.
	AutoApproved []string `json:"auto_approved,omitempty"`
}

// SpecLayout is the subset of config.Layout the gate binary needs:
// just where to append commands.jsonl. We don't pass the whole
// Layout struct because that would couple the gate binary to the
// agents config package.
type SpecLayout struct {
	SessionCommandsPath string `json:"session_commands_path"`
}

// HookEnvVar is the env-var name through which the parent binary
// tells the gate binary where to find its Spec file. Picked up by
// the gate binary at startup.
const HookEnvVar = "GATE_SPEC"

// claudeHookConfig is the JSON shape claude expects in its settings
// file under .hooks.PreToolUse.
//
// Reference: claude hooks-guide. PreToolUse fires before any tool
// invocation; matcher="Bash" filters to shell commands only. The
// hook command is the absolute path to the gate binary; exit 0 =
// allow, exit 2 = block.
type claudeHookConfig struct {
	Hooks claudeHooks `json:"hooks"`
}

type claudeHooks struct {
	PreToolUse []claudeHookGroup `json:"PreToolUse"`
}

type claudeHookGroup struct {
	Matcher string             `json:"matcher"`
	Hooks   []claudeHookEntry  `json:"hooks"`
}

type claudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// ClaudeSettings produces the JSON bytes claude expects in its
// `--settings` file. gateBin is the absolute path to the gate
// binary (we don't rely on PATH lookup so a per-test build can be
// used in integration tests).
func ClaudeSettings(gateBin string) ([]byte, error) {
	cfg := claudeHookConfig{
		Hooks: claudeHooks{
			PreToolUse: []claudeHookGroup{{
				Matcher: "Bash",
				Hooks: []claudeHookEntry{{
					Type:    "command",
					Command: gateBin,
				}},
			}},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// WriteSpawnArtifacts writes both:
//
//   - <dir>/spec.json   — the Spec consumed by the gate binary via $GATE_SPEC
//   - <dir>/settings.json — the claude --settings file
//
// Returns the settings path (caller passes to ClaudeSpawner.SettingsPath)
// and the spec path (caller injects into ExtraEnv as GATE_SPEC=<path>).
func WriteSpawnArtifacts(dir string, spec Spec, gateBin string) (settingsPath, specPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	specPath = filepath.Join(dir, "spec.json")
	settingsPath = filepath.Join(dir, "settings.json")

	specBytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(specPath, specBytes, 0o644); err != nil {
		return "", "", err
	}

	settingsBytes, err := ClaudeSettings(gateBin)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		return "", "", err
	}
	return settingsPath, specPath, nil
}

// LoadSpec reads the Spec file pointed to by $GATE_SPEC. Used by
// the gate binary at startup. Returns a clear error if the env var
// is unset so misconfiguration is loud.
func LoadSpec() (Spec, error) {
	path := os.Getenv(HookEnvVar)
	if path == "" {
		return Spec{}, fmt.Errorf("%s not set in env", HookEnvVar)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read spec %q: %w", path, err)
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("parse spec %q: %w", path, err)
	}
	return s, nil
}
