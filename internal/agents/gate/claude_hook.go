package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Spec is the per-app gate config the binary loads at every
// invocation. Single shared file at SharedSpecPath(AppName) — the
// gate binary discovers it from compile-time AppName, not runtime
// env. Rewritten by the daemon when the user toggles always-allow /
// revoke or edits allowed_cmds.
//
// Pre-Stage 9 this struct also carried session-scoped fields
// (SessionID, AgentName, SocketPath, SessionCommandsPath); those are
// gone now — gate is session-agnostic, daemon routes approvals via
// the cwd in the ApprovalRequest.
type Spec struct {
	Rules []CommandRule `json:"rules"`

	// AutoApproved holds matchKey hashes the user already chose
	// "Always allow" for. The gate binary checks this list before
	// dialing the socket so always-approved commands take a zero-
	// latency fast path identical to whitelisted ones.
	AutoApproved []string `json:"auto_approved,omitempty"`

	// DefaultScope is the filesystem path used as the scope for rules
	// that have an empty Scope field. Typically the default workspace
	// directory (~/.<app>/agents/workspaces/default/files). When
	// empty, rules with no scope are unrestricted (legacy behaviour).
	DefaultScope string `json:"default_scope,omitempty"`
}

// SharedSpecPath returns the on-disk location of the shared gate
// spec for an app. AppName empty falls back to the wick default.
//
// Layout: ~/.<app>/agents/gate/spec.json
func SharedSpecPath(appName string) string {
	return filepath.Join(sharedGateDir(appName), "spec.json")
}

// SharedSocketPath returns the Unix domain socket address the gate
// dials and the daemon listens on. Single shared socket per app —
// daemon multiplexes requests by inspecting cwd in the payload.
//
// Layout: ~/.<app>/agents/gate/gate.sock
func SharedSocketPath(appName string) string {
	return filepath.Join(sharedGateDir(appName), "gate.sock")
}

// SharedCommandsPath returns the global commands.jsonl audit log.
// Pre-Stage 9 this lived per-session under
// `~/.<app>/agents/sessions/<id>/commands.jsonl`; now it's a single
// app-wide file. UI Commands tab reads from here.
//
// Layout: ~/.<app>/agents/gate/commands.jsonl
func SharedCommandsPath(appName string) string {
	return filepath.Join(sharedGateDir(appName), "commands.jsonl")
}

// sharedGateDir returns ~/.<app>/agents/gate, falling back to
// ./.<app>/agents/gate when the home dir lookup fails so we never
// panic. Caller is responsible for MkdirAll.
func sharedGateDir(appName string) string {
	name := appName
	if name == "" {
		name = "wick"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "."+name, "agents", "gate")
	}
	return filepath.Join(home, "."+name, "agents", "gate")
}

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
	Matcher string            `json:"matcher"`
	Hooks   []claudeHookEntry `json:"hooks"`
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
	hook := claudeHookEntry{Type: "command", Command: gateBin}
	// Gate every file-system tool that can read/write outside the workspace.
	matchers := []string{"Bash", "Read", "Write", "Edit", "Glob"}
	groups := make([]claudeHookGroup, 0, len(matchers))
	for _, m := range matchers {
		groups = append(groups, claudeHookGroup{
			Matcher: m,
			Hooks:   []claudeHookEntry{hook},
		})
	}
	cfg := claudeHookConfig{
		Hooks: claudeHooks{PreToolUse: groups},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// WriteClaudeSettings writes the per-spawn `--settings` file claude
// expects. Returns the absolute path so the caller can pass it to
// ClaudeSpawner.SettingsPath.
//
// Pre-Stage 9 this lived inside WriteSpawnArtifacts which also wrote
// the spec — now spec is shared (see WriteSharedSpec) and only the
// settings file is per-spawn.
func WriteClaudeSettings(dir, gateBin string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	settingsPath := filepath.Join(dir, "settings.json")
	settingsBytes, err := ClaudeSettings(gateBin)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		return "", err
	}
	return settingsPath, nil
}

// WriteSharedSpec persists the shared spec for appName atomically.
// Caller passes the already-merged spec — appendAlwaysAllow /
// RevokeAlways handles read-modify-write in the daemon.
func WriteSharedSpec(appName string, spec Spec) error {
	path := SharedSpecPath(appName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir gate dir: %w", err)
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write spec %s: %w", path, err)
	}
	return nil
}

// LoadSpec reads the shared Spec for appName from disk. Used by the
// gate binary at every invocation. Missing file returns an empty
// Spec without error — that means "no rules configured", which the
// matcher treats as deny-all (fail-safe block).
func LoadSpec(appName string) (Spec, error) {
	path := SharedSpecPath(appName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Spec{}, nil
		}
		return Spec{}, fmt.Errorf("read spec %q: %w", path, err)
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("parse spec %q: %w", path, err)
	}
	return s, nil
}
