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

	"github.com/yogasw/wick/internal/agents/agent"
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
func (s Spawner) Spawn(ctx context.Context, opt agent.SpawnOptions) (agent.Process, error) {
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
	if s.SettingsPath != "" {
		args = append(args, "--settings", s.SettingsPath)
	}
	args = append(args, s.ExtraArgs...)
	if opt.ResumeID != "" {
		args = append(args, "--resume", opt.ResumeID)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = opt.Workspace
	cmd.Env = append(os.Environ(), opt.ExtraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Stderr is folded into the parent's stderr — claude doesn't
	// normally write stream-json to stderr, but if it does we want
	// the operator to see it rather than dropping it on the floor.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start claude: %w", err)
	}
	return &process{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// process implements agent.Process for a started claude subprocess.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Wait() error           { return p.cmd.Wait() }

func (p *process) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
