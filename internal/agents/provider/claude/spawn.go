// Package claude is the Claude-CLI specific Spawner implementation.
//
// Why a sub-package: keeping CLI-specific argv / env / flag math out
// of the core agent package lets phase 6 add codex / gemini siblings
// (`agent/codex`, `agent/gemini`) without touching agent.go. Each CLI
// owns its own folder with its own spawner, parser wiring, and
// CLI-version regression tests.
//
// Lifecycle: claude is invoked in headless streaming mode
// (`-p --input-format stream-json --output-format stream-json
// --verbose`). The subprocess stays alive across many turns within
// a session: each user message is one stdin line, each response is a
// burst of stdout events terminated by a `result` event. The agent
// idle timer kills the process when no events arrive for the TTL —
// the next user message respawns with `--resume <session_id>` so
// claude reads its own per-cwd history from ~/.claude/projects/ and
// picks up where it left off.
//
// The agent package depends on the agent.Spawner interface; this
// package satisfies it via Spawner.
package claude

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/capability"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/pkg/safeexec"
)

// Spawner spawns the real `claude` CLI binary with stream-json output
// and the resume flag when a CLI session ID is available.
//
// Binary defaults to `claude` (PATH lookup); operators can override
// via the Binary field for non-standard installs.
type Spawner struct {
	Binary string // empty → "claude"
	// BypassPermissions forces --permission-mode bypassPermissions and
	// disables the gate hook entirely for this spawn. Set ONLY for non-
	// interactive channels (Slack/HTTP) where no human can answer a
	// permission prompt and the operator has accepted unguarded
	// execution. Mutually exclusive with the per-instance gate hook:
	// applyHookConfig refuses to install the hook when this is true,
	// and the Spawn argv only adds the flag when gateActive=false.
	//
	// Why mutually exclusive: claude 2.1.138+ fires PreToolUse hooks
	// under bypassPermissions but ignores their deny envelope — every
	// blocked command runs anyway. Pre-2.1.138 behaviour (flag skips
	// hooks) flipped to (flag overrides deny). Either way, combining
	// them gives the user gate alerts they can't enforce.
	BypassPermissions bool
	// ExtraArgs is appended after the canonical headless flags, before
	// any caller-supplied ResumeID. Useful for tests / debugging
	// (--debug, --verbose-extra, ...).
	ExtraArgs []string
	// MCPToken points the agent at the live wick MCP HTTP server
	// (loopback) instead of cold-starting `wick mcp serve` per run.
	MCPToken string
}

// Spawn starts the subprocess.
//
// Argv shape (verified against claude 2.1.x):
//
//	claude -p --verbose
//	       --input-format stream-json
//	       --output-format stream-json
//	       [--permission-mode bypassPermissions]
//	       [--resume <id>]
//
// `-p` plus `stream-json` keeps the process alive in a long-lived
// streaming mode: stdin accepts one user envelope per turn, stdout
// emits one or more `system`/`assistant`/`result` events per turn,
// and the process stays open for follow-up turns until the agent
// idle TTL (or external kill).
//
// `--verbose` is required by claude when `-p` is paired with
// `--output-format stream-json` — claude errors out otherwise.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	bin := s.Binary
	if bin == "" {
		bin = "claude"
	}
	resolved, err := safeexec.ResolveBin(bin)
	if err != nil {
		return nil, fmt.Errorf("claude binary not found: %w", err)
	}
	bin = resolved

	// Install / remove the per-workspace hook config from the user's
	// per-instance intent. We do this every Spawn (not just the first)
	// so toggling Enabled in the UI takes effect on the next spawn
	// without restart, and toggling OFF correctly cleans up the stale
	// hook config from the previous run.
	gateActive := s.applyHookConfig(opt)

	args := []string{
		"-p",
		"--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		// Stream partial assistant text as it arrives so the UI shows a
		// live typing effect (matches the VSCode/TUI experience). Verified
		// safe against claude 2.1.138 — full tool_use + text turn streams
		// end-to-end without crash. Earlier exit-status-1 reports were
		// caused by a stale --resume ID, not this flag.
		"--include-partial-messages",
	}
	// Add the live wick MCP HTTP server when wired + supported. By
	// default it MERGES with the user's existing MCP servers (no
	// --strict-mcp-config) so their own connectors keep working; set
	// WICK_STRICT_MCP to isolate to just the wick server.
	if s.MCPToken != "" && os.Getenv("WICK_DISABLE_SHARED_MCP") == "" {
		if endpoint := mcpEndpointFromEnv(); endpoint != "" && mcpConfigSupported(bin) {
			strict := os.Getenv("WICK_STRICT_MCP") != "" && strictMCPConfigSupported(bin)
			// The session id is the basename of the per-session storage dir.
			// Sending it as a header lets the MCP server attribute connector
			// calls to this session without the LLM having to pass it.
			sessionID := ""
			if opt.SessionDir != "" {
				sessionID = filepath.Base(opt.SessionDir)
			}
			args = append(args, mcpConfigArgs(endpoint, s.MCPToken, sessionID, strict)...)
		}
	}
	// Trust the workspace explicitly so claude doesn't refuse to run
	// inside an "untrusted" directory. Without this, agent sessions
	// outside ~/.claude/projects/ get a workspace-trust block before
	// any tool fires.
	if opt.Workspace != "" {
		args = append(args, "--add-dir", opt.Workspace)
	}
	// Trust ~/.claude/skills so the agent can read a skill's bundled
	// resource files (they live outside the workspace).
	if home, err := os.UserHomeDir(); err == nil {
		args = append(args, skillAddDirArgs(home, dirExists)...)
	}
	// bypassPermissions and gate are mutually exclusive:
	//
	//   - gateActive=true  → DO NOT pass bypassPermissions. Claude
	//     2.1.138+ ignores the gate's deny envelope when this flag is
	//     set (hook still fires, but the tool runs anyway — verified
	//     against `mkdir 125` regression). The gate hook is the sole
	//     authority; claude defers to it without any extra flag.
	//   - s.BypassPermissions=true (and gate off) → pass the flag.
	//     Used by non-interactive channels (Slack/HTTP) where no human
	//     can answer a permission prompt. applyHookConfig already
	//     refuses to install the hook in this mode, so they never
	//     coexist on a live spawn.
	if !gateActive && s.BypassPermissions {
		args = append(args, "--permission-mode", "bypassPermissions")
	}
	args = append(args, s.ExtraArgs...)
	args = append(args, opt.ExtraArgs...)
	// 9router: when enabled, front the Anthropic API with wick's /9router/v1
	// proxy. Everything (base URL, auth token, per-tier models) is wired via
	// env in spawnEnv using Claude Code's own gateway vars — no --model on
	// argv. Models are optional (omitted when unset). Only the proxy base
	// URL is required, so fail fast if WICK_PORT is unset.
	if opt.Instance != nil && opt.Instance.Use9router {
		if provider.Router9BaseURL() == "" {
			return nil, fmt.Errorf("claude 9router: WICK_PORT unset — cannot resolve proxy base URL")
		}
	}
	args = append(args, maxTurnsArgs(opt.MaxTurns)...)
	if opt.Preset != "" {
		args = append(args, "--append-system-prompt", opt.Preset)
	}
	if opt.ResumeID != "" {
		args = append(args, "--resume", opt.ResumeID)
	}

	cmd := safeexec.CommandContext(ctx, bin, args...)
	cmd.Dir = opt.Workspace
	cmd.Env = spawnEnv(os.Environ(), opt)
	hideConsole(cmd)

	// Env wick injected on top of the inherited environment (ExtraEnv,
	// thinking, 9router), masked, for the spawn log. Passing a nil base to
	// spawnEnv yields only the added entries.
	addedEnv := provider.MaskSpawnEnv(spawnEnv(nil, opt))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Optional stdout tee: dump every line to the log too, so tests
	// can inspect what claude actually emitted without competing with
	// the parser for the pipe.
	if logPath := os.Getenv("WICK_CLAUDE_STDOUT_LOG"); logPath != "" {
		stdout = tee(stdout, logPath)
	}
	// Stderr is folded into the parent's stderr by default — claude
	// doesn't normally write stream-json to stderr, but if it does we
	// want the operator to see it rather than dropping it on the
	// floor. WICK_CLAUDE_STDERR_LOG can redirect to a file for tests
	// that need to inspect it.
	stderrB := newStderrTail(4096)
	var stderrDest io.Writer = os.Stderr
	if logPath := os.Getenv("WICK_CLAUDE_STDERR_LOG"); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			stderrDest = f
		}
	}
	// Tee stderr to its destination + a bounded tail buffer so an abnormal
	// exit can surface the real failure instead of a blank "agent error: ".
	cmd.Stderr = io.MultiWriter(stderrB, stderrDest)

	log.Info().
		Str("bin", bin).
		Strs("argv", args).
		Str("cwd", opt.Workspace).
		Str("resume", opt.ResumeID).
		Msg("agents.spawn: starting")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		log.Error().
			Err(err).
			Str("bin", bin).
			Msg("agents.spawn: start failed (set provider.Binary in /tools/agents/providers if claude not on PATH)")
		return nil, fmt.Errorf("start claude: %w", err)
	}
	log.Info().
		Int("pid", cmd.Process.Pid).
		Str("bin", bin).
		Msg("agents.spawn: started")
	return &process{cmd: cmd, stdin: stdin, stdout: stdout, stderrB: stderrB, env: addedEnv}, nil
}

// spawnEnv assembles the subprocess environment from base (typically
// os.Environ()) plus opt.ExtraEnv, appending MAX_THINKING_TOKENS=<value>
// when opt.ThinkingTokens is non-empty so claude caps (value "<n>") or
// disables (value "0") extended thinking. The thinking entry is purely
// additive: a spawn with an empty ThinkingTokens (the chat / default path)
// gets no MAX_THINKING_TOKENS env at all, so chat behaviour is unchanged.
// ExtraEnv is copied, never mutated.
func spawnEnv(base []string, opt provider.SpawnOptions) []string {
	env := append(append([]string{}, base...), opt.ExtraEnv...)
	if opt.ThinkingTokens != "" {
		env = append(env, "MAX_THINKING_TOKENS="+opt.ThinkingTokens)
	}
	// 9router: point Claude Code at wick's /9router/v1 proxy using its own
	// gateway env vars. ANTHROPIC_AUTH_TOKEN (NOT _API_KEY — that is for
	// direct Anthropic) carries the 9router key; the per-tier models map via
	// ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL. Each tier is emitted only
	// when its slot is set — an empty slot is skipped so the CLI keeps its
	// own default (no fallback to the primary model).
	if opt.Instance != nil && opt.Instance.Use9router {
		if base := provider.Router9BaseURL(); base != "" {
			env = append(env,
				"ANTHROPIC_BASE_URL="+base,
				"ANTHROPIC_AUTH_TOKEN="+provider.Router9AuthKey(*opt.Instance),
			)
			if opus := strings.TrimSpace(opt.Instance.Router9Models["opus"]); opus != "" {
				env = append(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="+opus)
			}
			if sonnet := strings.TrimSpace(opt.Instance.Router9Models["sonnet"]); sonnet != "" {
				env = append(env, "ANTHROPIC_DEFAULT_SONNET_MODEL="+sonnet)
			}
			if haiku := strings.TrimSpace(opt.Instance.Router9Models["haiku"]); haiku != "" {
				env = append(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL="+haiku)
			}
		}
	}
	return env
}

// applyHookConfig installs or removes the per-workspace hook config
// based on the per-instance user intent for the PreToolUse event.
// Returns true when the hook was (or already is) installed for this
// spawn — the caller uses that signal to flip --permission-mode
// bypassPermissions so claude defers all gating to the hook.
//
// Failure to install is a hard error in spirit but a soft one in
// practice: we log and return false so the spawn still proceeds
// without the gate rather than refusing to start the provider. The
// alternative (fail spawn) would block every session whenever the
// gate binary moved or .claude/ became unwritable, which is worse UX
// than degraded enforcement that's visible in logs.
func (s Spawner) applyHookConfig(opt provider.SpawnOptions) bool {
	if opt.Workspace == "" {
		return false
	}
	writer, ok := capability.LookupHookConfigWriter("claude")
	if !ok {
		// Adapter not registered — should never happen since
		// claude/capability_init.go registers in init(). Defensive
		// branch so a bad import graph degrades gracefully.
		return false
	}
	// Bypass mode owns the spawn outright: claude is told to skip every
	// permission prompt, so a gate hook would just emit alerts that the
	// user can't (and shouldn't need to) answer. Strip any stale hook
	// config from a previous gate-on spawn and run unguarded.
	if s.BypassPermissions {
		if err := writer.Remove(opt.Workspace); err != nil {
			log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: claude hook config remove failed (bypass mode)")
		}
		return false
	}
	// Hook install gated solely on the per-instance flag. The master
	// switch (when present) materialises into per-instance flags at
	// toggle time — spawner stays simple, single source of truth lives
	// in Instance.Hooks.
	enabled := opt.Instance != nil && opt.Instance.HookEnabled(provider.HookEventPreToolUse)
	if !enabled {
		if err := writer.Remove(opt.Workspace); err != nil {
			log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: claude hook config remove failed")
		}
		return false
	}
	if opt.GateBinary == "" {
		log.Warn().Str("workspace", opt.Workspace).Msg("agents.spawn: claude hook requested but gate binary path empty — running without gate")
		return false
	}
	if err := writer.Write(opt.Workspace, opt.GateBinary); err != nil {
		log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: claude hook config write failed")
		return false
	}
	return true
}

// tee returns a wrapped ReadCloser that mirrors all bytes into
// `path` (append) as they pass through. Used by debug tests via
// WICK_CLAUDE_STDOUT_LOG.
func tee(r io.ReadCloser, path string) io.ReadCloser {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return r
	}
	return &teeReader{src: r, f: f}
}

type teeReader struct {
	src io.ReadCloser
	f   *os.File
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if n > 0 {
		_, _ = t.f.Write(p[:n])
	}
	return n, err
}
func (t *teeReader) Close() error { _ = t.f.Close(); return t.src.Close() }

// process implements provider.Process for a started claude subprocess.
type process struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderrB *stderrTail
	env     []string // wick-injected env (masked), for the spawn log
}

func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Env() []string         { return p.env }
func (p *process) Wait() error           { return p.cmd.Wait() }

// StderrTail returns the tail of the subprocess's stderr (optional
// provider.StderrTailer) so the reader can log the real failure on exit.
func (p *process) StderrTail() string { return p.stderrB.String() }
func (p *process) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
func (p *process) Binary() string {
	if p.cmd == nil {
		return ""
	}
	return p.cmd.Path
}
func (p *process) Argv() []string {
	if p.cmd == nil || len(p.cmd.Args) <= 1 {
		return nil
	}
	return maskSensitiveArgs(p.cmd.Args[1:])
}

// maskSensitiveArgs truncates the value of --append-system-prompt to the
// first 5 words followed by "…" so logs show a readable preview without
// exposing the full preset content.
func maskSensitiveArgs(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--append-system-prompt" && i+1 < len(argv) {
			out = append(out, argv[i], truncate5Words(argv[i+1]))
			i++
			continue
		}
		out = append(out, argv[i])
	}
	return out
}

func truncate5Words(s string) string {
	fields := strings.Fields(s)
	if len(fields) <= 5 {
		return s
	}
	return strings.Join(fields[:5], " ") + "…"
}

func (p *process) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
