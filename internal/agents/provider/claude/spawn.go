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

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/capability"
	provider "github.com/yogasw/wick/internal/agents/provider"
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
	}
	// Trust the workspace explicitly so claude doesn't refuse to run
	// inside an "untrusted" directory. Without this, agent sessions
	// outside ~/.claude/projects/ get a workspace-trust block before
	// any tool fires.
	if opt.Workspace != "" {
		args = append(args, "--add-dir", opt.Workspace)
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
	if opt.ResumeID != "" {
		args = append(args, "--resume", opt.ResumeID)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = opt.Workspace
	cmd.Env = append(os.Environ(), opt.ExtraEnv...)
	hideConsole(cmd)

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
	if logPath := os.Getenv("WICK_CLAUDE_STDERR_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			cmd.Stderr = f
		} else {
			cmd.Stderr = os.Stderr
		}
	} else {
		cmd.Stderr = os.Stderr
	}

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
	return &process{cmd: cmd, stdin: stdin, stdout: stdout}, nil
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
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Wait() error           { return p.cmd.Wait() }
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
	out := make([]string, len(p.cmd.Args)-1)
	copy(out, p.cmd.Args[1:])
	return out
}

func (p *process) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
