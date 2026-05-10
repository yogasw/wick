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
	provider "github.com/yogasw/wick/internal/agents/provider"
)

// Spawner spawns the real `claude` CLI binary with stream-json output
// and the resume flag when a CLI session ID is available.
//
// Binary defaults to `claude` (PATH lookup); operators can override
// via the Binary field for non-standard installs. SettingsPath is the
// `--settings <path>` value injected by phase 3 gate; empty = no
// override.
type Spawner struct {
	Binary       string // empty → "claude"
	SettingsPath string // empty → no --settings flag
	// BypassPermissions forces --permission-mode bypassPermissions even
	// when no gate settings file is present. Use when running Claude in
	// non-interactive contexts (Slack/HTTP) without a gate configured.
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
//	       [--settings <path>]
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
	if s.SettingsPath != "" {
		// Gate is active: bypass interactive prompts so the PreToolUse
		// hook is the sole allow/block authority.
		args = append(args, "--permission-mode", "bypassPermissions")
		args = append(args, "--settings", s.SettingsPath)
	} else if s.BypassPermissions {
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
