// Package gemini implements provider.Spawner for Google's Gemini CLI.
//
// Status: experimental. The hook contract, sandboxing flags, and
// resume semantics are taken from publicly available docs and have
// NOT been verified end-to-end against an installed binary yet. The
// capability registry exposes this as "untested" so the UI surfaces
// that risk to operators before they enable the gate for gemini.
//
// When someone with gemini access verifies this code path, they should:
//
//   - confirm the argv shape against `gemini --help` of their installed
//     version and update [Spawner.Spawn] if it has drifted,
//   - flip the InterceptScope in capability_init.go from "untested" to
//     whatever the verified scope is ("bash+edit+mcp" / "shell-only"),
//   - drop the t.Skip in the integration tests under provider/gemini/.
package gemini

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

// Spawner spawns the real `gemini` CLI binary in non-interactive mode.
//
// Binary defaults to `gemini` (PATH lookup). YoloMode mirrors gemini's
// approval-bypass flag (`--yolo` per current docs) and follows the
// same "set only when no gate" rule as the other providers.
type Spawner struct {
	Binary    string // empty → "gemini"
	YoloMode  bool   // --yolo: skip every approval prompt. Set ONLY when no gate.
	ExtraArgs []string
}

// Spawn starts the subprocess.
//
// Argv shape (target — UNVERIFIED, treat as best-effort until a
// gemini-enabled contributor confirms against `gemini --help`):
//
//	gemini -p
//	       [--yolo]
//	       [--resume <id>]
//
// `-p` is the headless / non-interactive entry per the public
// reference; `--yolo` skips approvals; `--resume` carries the session
// id forward across spawns when wick re-attaches.
//
// TODO(gemini-verify): replace this comment block with the verified
// argv shape once someone with the binary runs `gemini --help` and
// confirms (or corrects) the flag names.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	bin := s.Binary
	if bin == "" {
		bin = "gemini"
	}

	// Install / remove per-workspace hook config based on the user's
	// per-instance intent. See claude/spawn.go for the fail-soft
	// rationale. UNVERIFIED: gemini hook contract not validated end-
	// to-end; the writer + prober still ship so the wiring is
	// exercised even when no contributor has confirmed the format.
	gateActive := s.applyHookConfig(opt)

	args := []string{"-p"}
	// YoloMode skips every approval prompt — incompatible with gate
	// enforcement because gemini in yolo mode is believed to bypass
	// hook firing. Defer to the hook envelope when gate is active.
	if !gateActive && s.YoloMode {
		args = append(args, "--yolo")
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
		Msg("agents.spawn: starting (gemini)")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		log.Error().Err(err).Str("bin", bin).Msg("agents.spawn: gemini start failed")
		return nil, fmt.Errorf("start gemini: %w", err)
	}
	log.Info().Int("pid", cmd.Process.Pid).Str("bin", bin).Msg("agents.spawn: started (gemini)")
	return &process{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// applyHookConfig installs / removes the per-workspace hook config
// based on the user's per-instance intent for PreToolUse. Returns
// true when the hook is installed for this spawn. Same fail-soft
// approach as the claude/codex equivalents.
func (s Spawner) applyHookConfig(opt provider.SpawnOptions) bool {
	if opt.Workspace == "" {
		return false
	}
	writer, ok := capability.LookupHookConfigWriter("gemini")
	if !ok {
		return false
	}
	enabled := opt.Instance != nil && opt.Instance.HookEnabled(provider.HookEventPreToolUse)
	if !enabled {
		if err := writer.Remove(opt.Workspace); err != nil {
			log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: gemini hook config remove failed")
		}
		return false
	}
	if opt.GateBinary == "" {
		log.Warn().Str("workspace", opt.Workspace).Msg("agents.spawn: gemini hook requested but gate binary path empty")
		return false
	}
	if err := writer.Write(opt.Workspace, opt.GateBinary); err != nil {
		log.Warn().Err(err).Str("workspace", opt.Workspace).Msg("agents.spawn: gemini hook config write failed")
		return false
	}
	return true
}

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
