package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Spec describes one gate setup for one spawn — the bundle of paths
// + env that wick-gate needs to make a decision and log it.
//
// Generated per Spawn by pool/factory.go; written to a file under
// the session's gate-temp dir so the path can be passed to claude
// via `--settings <path>` and to wick-gate via env vars.
type Spec struct {
	SessionID    string        `json:"session_id"`
	AgentName    string        `json:"agent_name"`
	Layout       SpecLayout    `json:"layout"`
	Rules        []CommandRule `json:"rules"`
}

// SpecLayout is the subset of config.Layout the gate binary needs:
// just where to append commands.jsonl. We don't pass the whole
// Layout struct because that would couple the gate binary to the
// agents config package.
type SpecLayout struct {
	SessionCommandsPath string `json:"session_commands_path"`
}

// HookEnvVar is the env-var name through which the wick binary tells
// wick-gate where to find its Spec file. Picked up by wick-gate at
// startup.
const HookEnvVar = "WICK_GATE_SPEC"

// claudeHookConfig is the JSON shape claude expects in its settings
// file under .hooks.PreToolUse.
//
// Reference: claude hooks-guide. PreToolUse fires before any tool
// invocation; matcher="Bash" filters to shell commands only. The
// hook command is the absolute path to wick-gate; exit 0 = allow,
// exit 2 = block.
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
// `--settings` file. wickGateBin is the absolute path to the
// wick-gate binary (we don't rely on PATH lookup so a per-test
// build can be used in integration tests).
func ClaudeSettings(wickGateBin string) ([]byte, error) {
	cfg := claudeHookConfig{
		Hooks: claudeHooks{
			PreToolUse: []claudeHookGroup{{
				Matcher: "Bash",
				Hooks: []claudeHookEntry{{
					Type:    "command",
					Command: wickGateBin,
				}},
			}},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// WriteSpawnArtifacts writes both:
//
//   - <dir>/spec.json   — the Spec consumed by wick-gate via $WICK_GATE_SPEC
//   - <dir>/settings.json — the claude --settings file
//
// Returns the settings path (caller passes to ClaudeSpawner.SettingsPath)
// and the spec path (caller injects into ExtraEnv as WICK_GATE_SPEC=<path>).
func WriteSpawnArtifacts(dir string, spec Spec, wickGateBin string) (settingsPath, specPath string, err error) {
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

	settingsBytes, err := ClaudeSettings(wickGateBin)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		return "", "", err
	}
	return settingsPath, specPath, nil
}

// LoadSpec reads the Spec file pointed to by $WICK_GATE_SPEC. Used
// by wick-gate at startup. Returns a clear error if the env var is
// unset so misconfiguration is loud.
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
