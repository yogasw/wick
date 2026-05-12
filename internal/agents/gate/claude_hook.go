package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
//
// On Windows, Claude Code invokes hook commands via /usr/bin/bash
// (WSL or Git Bash). Backslashes in the path are stripped by bash,
// turning C:\foo\gate.exe into C:foogate.exe (exit 127). Convert
// all backslashes to forward slashes so the path survives bash on
// all platforms.
func ClaudeSettings(gateBin string) ([]byte, error) {
	gateBin = strings.ReplaceAll(gateBin, "\\", "/")
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

// WriteWorkspaceHooks writes the gate hook into
// <workspace>/.claude/settings.local.json so Claude's project-scoped
// hook loader picks it up. Claude does NOT honour hooks injected via
// the --settings flag; they must live in the standard settings
// hierarchy (.claude/settings.json or .claude/settings.local.json).
// We use the .local variant to avoid conflicting with any
// settings.json the user may have committed to the workspace.
// Idempotent: overwrites an existing file with the same content.
func WriteWorkspaceHooks(workspace, gateBin string) error {
	dir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := ClaudeSettings(gateBin)
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, "settings.local.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
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

// ProbeResult is what ProbeGateSupport reports back to the UI. The
// shape is intentionally flat so the JSON survives a frontend JSON
// stringify without surprises.
type ProbeResult struct {
	// Supported is the headline answer: did claude honor the deny
	// envelope? True iff the probe file did NOT get created.
	Supported bool `json:"supported"`

	// Reason is a one-sentence explanation safe to show in a toast.
	Reason string `json:"reason"`

	// ClaudeVersion is the `claude --version` first line — we capture
	// it so the user can paste a bug report without re-running.
	ClaudeVersion string `json:"claude_version,omitempty"`

	// Stderr / Stdout from the probe spawn, truncated. Help the
	// operator debug "supported=false" without re-running on the CLI.
	Stderr string `json:"stderr,omitempty"`
	Stdout string `json:"stdout,omitempty"`

	// DurationMs is wall-clock for the spawn. UI can warn if probe
	// took unusually long (e.g. login interactive prompt blocked).
	DurationMs int64 `json:"duration_ms"`
}

// ProbeGateSupport runs a one-shot end-to-end check: does this
// `claude` build honor our PreToolUse hook's deny envelope?
//
// Why this exists: the hook contract has churned across claude
// releases (top-level `decision` → `hookSpecificOutput.permission
// Decision`, exit-2 → exit-0+JSON). A version check is brittle; the
// only reliable signal is "spawn claude, force-deny, see if the
// tool actually ran". This function does exactly that:
//
//  1. tempdir as workspace + sentinel file path inside it
//  2. write a `--settings` file routing PreToolUse[Bash] to
//     `<gateBin> --probe-deny`, which always emits the deny envelope
//  3. `claude -p --settings ... "create file <sentinel>"`
//  4. check if sentinel exists. Exists = claude ignored deny =
//     unsupported. Missing = supported.
//
// Returns Supported=false on any infra failure (claude not on PATH,
// timeout, etc.) so the UI can surface a single boolean. The Reason
// field disambiguates "claude broken" vs "gate bypassed".
func ProbeGateSupport(ctx context.Context, claudeBin, gateBin string) ProbeResult {
	start := time.Now()
	finish := func(r ProbeResult) ProbeResult {
		r.DurationMs = time.Since(start).Milliseconds()
		return r
	}

	if claudeBin == "" {
		return finish(ProbeResult{Supported: false, Reason: "claude binary not configured"})
	}
	if gateBin == "" {
		return finish(ProbeResult{Supported: false, Reason: "gate binary not resolved — run `wick build`"})
	}

	// Capture version best-effort; failure is non-fatal for the probe
	// itself.
	ver := claudeVersionString(ctx, claudeBin)

	dir, err := os.MkdirTemp("", "wick-gate-probe-*")
	if err != nil {
		return finish(ProbeResult{Supported: false, Reason: "mkdir temp: " + err.Error(), ClaudeVersion: ver})
	}
	defer os.RemoveAll(dir)

	// Settings file routes Bash through `<gate> --probe-deny`.
	settingsPath := filepath.Join(dir, "settings.json")
	hookCmd := strings.ReplaceAll(gateBin, "\\", "/") + " --probe-deny"
	cfg := claudeHookConfig{Hooks: claudeHooks{PreToolUse: []claudeHookGroup{
		{Matcher: "Bash", Hooks: []claudeHookEntry{{Type: "command", Command: hookCmd}}},
	}}}
	settingsBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return finish(ProbeResult{Supported: false, Reason: "marshal settings: " + err.Error(), ClaudeVersion: ver})
	}
	if err := os.WriteFile(settingsPath, settingsBytes, 0o644); err != nil {
		return finish(ProbeResult{Supported: false, Reason: "write settings: " + err.Error(), ClaudeVersion: ver})
	}

	// Sentinel: if claude executes the bash despite our deny, this
	// file appears. We use a path inside the workspace tempdir so the
	// probe leaves no trace outside.
	sentinel := filepath.Join(dir, "probe-sentinel.txt")
	sentinelMsys := "/" + strings.ReplaceAll(strings.Replace(sentinel, ":", "", 1), "\\", "/")
	if !strings.HasPrefix(sentinel, "/") {
		// On POSIX no rewrite needed.
		sentinelMsys = sentinel
	}

	prompt := fmt.Sprintf(`Run this exact bash command without asking: touch "%s"`, sentinelMsys)

	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, claudeBin,
		"-p",
		"--settings", settingsPath,
		"--output-format", "stream-json",
		"--verbose",
		prompt,
	)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	stdout := string(out)
	stderr := ""
	if err != nil {
		stderr = err.Error()
	}

	// Truncate to keep payload small for the UI.
	if len(stdout) > 2000 {
		stdout = stdout[:2000] + "...(truncated)"
	}

	_, statErr := os.Stat(sentinel)
	switch {
	case statErr == nil:
		return finish(ProbeResult{
			Supported:     false,
			Reason:        "claude ignored the deny envelope — sentinel file was created",
			ClaudeVersion: ver,
			Stdout:        stdout,
			Stderr:        stderr,
		})
	case os.IsNotExist(statErr):
		return finish(ProbeResult{
			Supported:     true,
			Reason:        "claude honored the deny envelope — Bash tool was cancelled",
			ClaudeVersion: ver,
			Stdout:        stdout,
			Stderr:        stderr,
		})
	default:
		return finish(ProbeResult{
			Supported:     false,
			Reason:        "stat sentinel: " + statErr.Error(),
			ClaudeVersion: ver,
			Stdout:        stdout,
			Stderr:        stderr,
		})
	}
}

// claudeVersionString runs `claude --version` and returns the first
// line, or "" on failure. Best-effort, short timeout — if version
// probe hangs we give up and let the caller proceed.
func claudeVersionString(ctx context.Context, claudeBin string) string {
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, claudeBin, "--version").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
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
