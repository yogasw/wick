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
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/capability"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/safeexec"
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
	resolved, err := safeexec.ResolveBin(bin)
	if err != nil {
		return nil, fmt.Errorf("codex binary not found: %w", err)
	}
	bin = resolved

	// Write preset to <session>/.codex/soul.md and point codex at it via
	// -c model_instructions_file so the instructions apply on both fresh
	// and resumed sessions (AGENTS.md is ignored on resume).
	//
	// soul.md MUST live under the per-session dir, not the project
	// Workspace: the preset embeds the session identity block
	// (session_id, channel), and many sessions can share one project
	// workspace — a workspace-local soul.md would clobber across
	// sessions and race on concurrent spawns, feeding codex the wrong
	// session_id. SessionDir is empty only in legacy/test paths, where
	// we fall back to the workspace.
	soulPath := ""
	if opt.Preset != "" {
		soulDir := opt.SessionDir
		if soulDir == "" {
			soulDir = opt.Workspace
		}
		if soulDir != "" {
			codexDir := filepath.Join(soulDir, ".codex")
			if err := os.MkdirAll(codexDir, 0o755); err == nil {
				p := filepath.Join(codexDir, "soul.md")
				if err := os.WriteFile(p, []byte(opt.Preset), 0o644); err == nil {
					soulPath = p
				} else {
					log.Warn().Err(err).Str("path", p).Msg("agents.spawn: write soul.md failed")
				}
			}
		}
	}

	// Install / remove per-workspace hook config based on the user's
	// per-instance intent. See claude/spawn.go applyHookConfig for the
	// fail-soft rationale.
	gateActive := s.applyHookConfig(opt)

	args := []string{
		"exec",
		"--json",
		"--skip-git-repo-check",
	}
	sandboxMode := provider.CodexSandboxFullAccess
	if opt.Instance != nil && opt.Instance.CodexConfig != nil && opt.Instance.CodexConfig.SandboxMode != "" {
		sandboxMode = opt.Instance.CodexConfig.SandboxMode
	}
	args = append(args, "--sandbox", string(sandboxMode))
	// When gate is active for this instance, do NOT set
	// --ask-for-approval to a bypass value — codex's approval flag
	// generally skips PreToolUse under bypass modes, which would
	// disable the gate. Defer to the hook envelope instead.
	if !gateActive && s.AskForApproval != "" {
		args = append(args, "--ask-for-approval", s.AskForApproval)
	}
	args = append(args, s.ExtraArgs...)
	args = append(args, opt.ExtraArgs...)
	if soulPath != "" {
		// model_instructions_file points codex at our preset file as the
		// model instructions. The earlier `instructions_files` key was
		// silently ignored by codex (unknown config key) — the model
		// never received soul.md at all, so it had no session identity
		// or wick rules. Value is a TOML literal string (single quotes)
		// so Windows backslashes in the path are not treated as escapes.
		args = append(args, "-c", `model_instructions_file=`+tomlLiteral(soulPath))
	}
	if opt.ResumeID != "" {
		args = append(args, "resume", opt.ResumeID)
	}
	if opt.InitialMessage != "" {
		args = append(args, opt.InitialMessage)
	}

	bin, args = termuxProotWrap(bin, args)

	cmd := safeexec.CommandContext(ctx, bin, args...)
	cmd.Dir = opt.Workspace
	cmd.Env = append(os.Environ(), opt.ExtraEnv...)
	hideConsole(cmd)

	// Codex reads its prompt from argv (positional [PROMPT]), never from
	// stdin. We must give the child an already-closed stdin so codex sees
	// immediate EOF and does not block on "Reading additional input from
	// stdin". A StdinPipe + Close() is NOT reliable here: on Windows the
	// codex.cmd batch → node wrapper does not always propagate the pipe
	// close as EOF, so the node process hangs forever. Setting Stdin to a
	// nil/closed reader makes Go wire the child's stdin to the OS null
	// device (NUL / /dev/null), which always EOFs instantly.
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
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
		log.Error().Err(err).Str("bin", bin).Msg("agents.spawn: codex start failed")
		return nil, fmt.Errorf("start codex: %w", err)
	}
	log.Info().Int("pid", cmd.Process.Pid).Str("bin", bin).Msg("agents.spawn: started (codex)")
	return &process{cmd: cmd, stdout: stdout}, nil
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

// tomlLiteral wraps s in a TOML literal string (single quotes) so
// backslashes and other escape-prone chars in a Windows path pass
// through verbatim. TOML literal strings cannot contain a single
// quote; if s does (not expected for wick-controlled session paths),
// fall back to a basic string with backslashes doubled.
func tomlLiteral(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// process implements provider.Process for a started codex subprocess.
// Codex takes its prompt from argv, so there is no stdin writer — Stdin()
// returns a no-op closer to satisfy the Process interface.
type process struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
}

func (p *process) Stdout() io.Reader     { return p.stdout }
func (p *process) Stdin() io.WriteCloser { return noopWriteCloser{} }

// noopWriteCloser discards writes; codex never reads stdin.
type noopWriteCloser struct{}

func (noopWriteCloser) Write(b []byte) (int, error) { return len(b), nil }
func (noopWriteCloser) Close() error                { return nil }
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

// termuxProotWrap wraps the codex argv with `proot` bind-mounts when
// running under Termux. Codex's musl binary hard-codes
// /etc/resolv.conf and /etc/ssl/certs/ca-certificates.crt; Android's
// /etc is read-only, so the websocket handshake to wss://chatgpt.com
// fails with "failed to lookup address information: Try again" (DNS)
// without bind-mounting Termux's copies into place.
//
// install.sh provisions proot and writes the same bind-mount as a
// bash alias for interactive use. Wick spawns codex via direct exec
// (alias bypassed), so the wrap has to happen here.
func termuxProotWrap(bin string, args []string) (string, []string) {
	if os.Getenv("TERMUX_VERSION") == "" {
		if _, err := os.Stat("/data/data/com.termux/files/usr"); err != nil {
			return bin, args
		}
	}
	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		return bin, args
	}
	wrapped := append([]string{
		"-b", prefix + "/etc/resolv.conf:/etc/resolv.conf",
		"-b", prefix + "/etc/tls/cert.pem:/etc/ssl/certs/ca-certificates.crt",
		bin,
	}, args...)
	log.Info().Str("bin", bin).Msg("agents.spawn: codex wrapped with proot (termux)")
	return "proot", wrapped
}
