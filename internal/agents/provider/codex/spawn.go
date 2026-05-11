// Package codex implements provider.Spawner for OpenAI's Codex CLI.
//
// The codex CLI is the second AI runtime wick supports after claude.
// It follows a similar headless-streaming model — long-lived
// subprocess, one prompt per stdin envelope, streamed JSON responses —
// but the argv shape and approval-flag semantics differ enough to need
// their own spawner rather than parameterising claude's.
//
// Hook integration (PreToolUse) is supported by codex but is still
// coarser than claude's: only "simple" shell commands fire the hook.
// That difference is surfaced via the capability registry, not here.
package codex

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

// Spawner spawns the real `codex` CLI binary in non-interactive
// `codex exec`-style mode.
//
// Binary defaults to `codex` (PATH lookup); operators can override via
// the Binary field for non-standard installs (npm-bin paths, custom
// builds).
type Spawner struct {
	Binary string // empty → "codex"
	// AskForApproval mirrors codex's --ask-for-approval flag. The
	// canonical "skip the terminal approval prompt" value is "never";
	// leave empty to let codex use its own default. Set ONLY when there
	// is no gate to fall back on — for non-interactive channels (Slack /
	// HTTP) where no human can answer a prompt. When the gate is active,
	// leave this empty: bypass modes generally skip PreToolUse hooks
	// (verify per release), which would silently disable the gate.
	AskForApproval string
	// ExtraArgs is appended after the canonical headless flags, before
	// any caller-supplied ResumeID. Useful for tests / debugging.
	ExtraArgs []string
}

// Spawn starts the subprocess.
//
// Argv shape (target — verify against `codex --help` of the installed
// version since OpenAI revises this surface frequently):
//
//	codex exec
//	      [--sandbox workspace-write]
//	      [--ask-for-approval <mode>]
//	      [--resume <id>]
//
// `codex exec` is the non-interactive entry point. `--sandbox
// workspace-write` keeps file mutations scoped to the spawn cwd,
// matching wick's expectation that one session = one workspace.
//
// ResumeID is forwarded as `--resume <id>` when present; an empty
// ResumeID starts a fresh codex session.
//
// TODO(codex 0.129+): confirm the exact flag names by running
// `codex exec --help` on the user's installed binary and adjust if the
// CLI has renamed --sandbox / --ask-for-approval (it has happened
// across minor releases).
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	bin := s.Binary
	if bin == "" {
		bin = "codex"
	}

	// Install / remove per-workspace hook config based on the user's
	// per-instance intent. See claude/spawn.go applyHookConfig for the
	// fail-soft rationale.
	gateActive := s.applyHookConfig(opt)

	args := []string{
		"exec",
		"--sandbox", "workspace-write",
	}
	// When gate is active for this instance, do NOT set
	// --ask-for-approval to a bypass value — codex's approval flag
	// generally skips PreToolUse under bypass modes, which would
	// disable the gate. Defer to the hook envelope instead.
	if !gateActive && s.AskForApproval != "" {
		args = append(args, "--ask-for-approval", s.AskForApproval)
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
	cmd.Stderr = os.Stderr

	log.Info().
		Str("bin", bin).
		Strs("argv", args).
		Str("cwd", opt.Workspace).
		Str("resume", opt.ResumeID).
		Msg("agents.spawn: starting (codex)")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		log.Error().Err(err).Str("bin", bin).Msg("agents.spawn: codex start failed")
		return nil, fmt.Errorf("start codex: %w", err)
	}
	log.Info().Int("pid", cmd.Process.Pid).Str("bin", bin).Msg("agents.spawn: started (codex)")
	return &process{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// applyHookConfig installs / removes the per-workspace hook config
// based on the user's per-instance intent for PreToolUse. Returns
// true when the hook is installed for this spawn. See the claude
// equivalent for the fail-soft rationale.
func (s Spawner) applyHookConfig(opt provider.SpawnOptions) bool {
	if opt.Workspace == "" {
		return false
	}
	writer, ok := capability.LookupHookConfigWriter("codex")
	if !ok {
		return false
	}
	enabled := opt.Instance != nil && opt.Instance.HookEnabled(provider.HookEventPreToolUse)
	if !enabled {
		if err := writer.Remove(opt.Workspace); err != nil {
			log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: codex hook config remove failed")
		}
		return false
	}
	if opt.GateBinary == "" {
		log.Warn().Str("workspace", opt.Workspace).Msg("agents.spawn: codex hook requested but gate binary path empty")
		return false
	}
	if err := writer.Write(opt.Workspace, opt.GateBinary); err != nil {
		log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: codex hook config write failed")
		return false
	}
	return true
}

// process implements provider.Process for a started codex subprocess.
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
