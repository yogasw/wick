// Package provider owns everything per-AI-CLI for the agents module:
//
//   - Agent lifecycle: spawn one CLI subprocess, pipe stdin/stdout,
//     run an idle timer, surface state, tear down on demand
//   - Spawner interface: pluggable subprocess construction so tests
//     can drive the agent without a real claude binary
//   - Type / Instance config: which CLIs are supported (claude /
//     codex / gemini), per-instance overrides (binary path, extra
//     args, env) read from userconfig
//   - Detect + `--version` probes used by the Backends UI page
//   - Per-spawn jsonl logs used by the Backends UI page
//
// Sub-packages `claude/`, `codex/`, `gemini/` provide the real
// CLI-specific Spawner implementations. They depend on this package
// for the Spawner / SpawnOptions interface; this package never
// imports them back.
package provider

import (
	"context"
	"io"
)

// Process is a started subprocess: stdout reader, stdin writer, and a
// Wait method that returns when the process exits.
//
// Implementations:
//   - exec.Cmd-backed (production)
//   - pipe-backed fake (tests)
//
// Stdout is the parser-facing stream — for claude that's stream-json.
// Wait MUST drain Stdout to EOF before returning so callers can rely
// on the read loop seeing every line.
type Process interface {
	Stdout() io.Reader
	Stdin() io.WriteCloser
	Wait() error
	Kill() error
	// Pid returns the OS process id of the started subprocess, or 0 if
	// not applicable (fake spawners in tests). Used by the spawn logger
	// + Backends UI to verify a re-spawn actually got a new process and
	// not just the same one looping.
	Pid() int
	// Binary is the resolved absolute path of the launched executable
	// (e.g. "/usr/local/bin/claude"). Empty when the spawner is a test
	// fake. Logged at spawn-start so operators can debug "claude not
	// found" / wrong binary issues from the Backends UI alone.
	Binary() string
	// Argv is the argument vector handed to the subprocess (excluding
	// argv[0] = binary). Logged at spawn-start so the operator can
	// reproduce the spawn manually outside wick.
	Argv() []string
	// Env returns the env vars wick INJECTED for this spawn (KEY=VALUE),
	// NOT the full inherited OS environment — just what the instance
	// config + provider wiring added (e.g. ANTHROPIC_BASE_URL, the 9router
	// key). Secret-looking values are masked. Logged at spawn-start so the
	// operator can verify routing/auth from the Backends UI. May be nil.
	Env() []string
}

// ScopedProcess is an optional interface a Process may implement to
// report the systemd scope it was launched inside.
//
// Kept off Process itself deliberately: nine types implement Process,
// most of them test fakes with no notion of a scope, and widening the
// interface would force an empty method onto all of them for the benefit
// of three. Callers type-assert and treat absence as "not scoped", which
// is exactly what an unwrapped spawn is.
type ScopedProcess interface {
	ScopeUnit() string
}

// scopeUnitOf returns the scope a process runs in, or "" when it is not
// scoped or does not report one.
func scopeUnitOf(p Process) string {
	if sp, ok := p.(ScopedProcess); ok {
		return sp.ScopeUnit()
	}
	return ""
}

// Spawner builds a Process from spawn parameters. The agent package
// asks the spawner to start a subprocess; the spawner is responsible
// for choosing argv, working directory, env, and any CLI-specific
// flags (e.g. claude's --output-format stream-json + --resume).
type Spawner interface {
	Spawn(ctx context.Context, opt SpawnOptions) (Process, error)
}

// SpawnOptions describes one spawn request. Workspace is the cwd of
// the subprocess (session worktree). ResumeID is the CLI-side session
// ID captured from a previous run; empty = start a fresh session.
//
// The agent package never reaches into the spawner internals — every
// CLI-flag decision happens inside the spawner, keeping agent.go
// CLI-agnostic and easier to extend with codex / gemini in phase 6.
type SpawnOptions struct {
	Workspace string
	ResumeID  string
	// SessionDir is the per-session storage dir (abs path). Providers
	// that materialise per-session files (e.g. codex's soul.md, which
	// embeds the session identity block) MUST write them here, not into
	// Workspace — multiple sessions can share one project workspace, so
	// a workspace-local file would clobber across sessions / race on
	// concurrent spawns. Empty = fall back to Workspace (tests).
	SessionDir string
	// SessionID is the session's real id, as everything else in wick
	// addresses it.
	//
	// Carried separately from SessionDir because THE PATH AND THE ID ARE
	// NOT THE SAME STRING, and nothing guarantees they ever will be. The
	// id is the identifier; the directory is where its bytes happen to
	// live, and Layout owns that mapping (see config.Layout.SessionDir).
	//
	// Sub-agents are where the two visibly diverge today: the directory
	// nests (sessions/<parent>/subagents/<seg>) while the id stays flat
	// (<parent>--sub-<seg>). Taking the basename of the path therefore
	// yielded a bare segment matching no session, and every op that looks
	// a delegation up from its own session — progress, report_result —
	// answered "this conversation is not a delegation". Supervision looked
	// wired and did nothing.
	//
	// So: pass it. A provider that re-derives an id from a path is
	// duplicating Layout's mapping in a place that will not be updated
	// when that mapping changes.
	SessionID string
	// ExtraEnv lets the gate (phase 3) inject hook config paths
	// without coupling the agent package to gate internals.
	// Instance.Env is merged in by the factory before every spawn.
	ExtraEnv []string
	// ExtraArgs is appended after each spawner's own ExtraArgs field.
	// Populated by the factory from Instance.ExtraArgs so UI-configured
	// extra flags are forwarded on every spawn without restarting wick.
	ExtraArgs []string

	// Instance is the resolved per-instance config the factory looked
	// up before this spawn. Spawners read Instance.Hooks to decide
	// which hook configs to install / remove on the workspace and
	// whether to flip provider-specific bypass flags. nil = legacy
	// test paths that don't drive hook plumbing.
	Instance *Instance

	// GateBinary is the absolute path to <app>-gate the spawner should
	// reference when writing hook configs. Resolved once by the
	// factory (sibling / embed / PATH) and forwarded so each provider
	// sub-package doesn't have to repeat the resolution dance.
	GateBinary string

	// Preset is the system prompt content injected via --append-system-prompt
	// when non-empty. Each provider spawner decides how to pass it to the
	// underlying CLI. The value is never written to spawn logs — Argv() strips it.
	Preset string

	// InitialMessage is the first user prompt for providers that take the
	// prompt as a positional arg (codex) rather than via stdin after spawn.
	// Empty = no positional prompt arg appended. claude ignores this field.
	InitialMessage string

	// SenderVisibility is the operator's setting for how much of a message
	// sender's identity reaches the model (store.SenderOff / SenderName /
	// SenderNameID / SenderFull). Empty = SenderName.
	//
	// Only providers that REPLAY conversation.jsonl themselves need this. The
	// pool prepends the `[from: …]` line on a live send, but the stored turn
	// keeps the sender as a structured field rather than in its text — so a
	// provider rebuilding a prompt from history has to re-apply the line, the
	// same way it re-appends attachment paths, or every replayed turn comes
	// back anonymous and a shared thread loses track of who said what.
	// Providers that resume via the CLI's own transcript ignore this.
	SenderVisibility string

	// MaxTurns caps agentic turns for this spawn (--max-turns on claude).
	// 0 = no cap. Threaded from the agent node's max_turns.
	MaxTurns int

	// ThinkingTokens is the resolved MAX_THINKING_TOKENS env value for this
	// spawn. Empty = leave it unset (full / provider-default thinking); "0"
	// = thinking disabled; "<n>" = explicit token budget. The claude spawner
	// injects MAX_THINKING_TOKENS=<value> only when non-empty; gemini/codex
	// spawners ignore it (documented no-op for now). Empty by default so the
	// regular agent chat flow is byte-identical — only the workflow agent
	// node sets it (from its thinking + max_thinking_tokens inputs).
	ThinkingTokens string

	// MemGuard is the resolved memory policy for this spawn. nil = the
	// guard is off, or the caller is a test fake; spawners then behave
	// exactly as they did before the guard existed.
	MemGuard *MemGuard

	// SpawnSeq names the scope unit uniquely — systemd refuses a duplicate
	// unit name while the first is still alive, so two concurrent spawns of
	// the same provider must not collide.
	SpawnSeq int

	// ToolMemoryMaxMB caps a shell command the wick provider runs itself
	// (grep, curl, a script), counting its whole process tree. 0 = no
	// limit. Separate from the agent ceiling because a tool that exceeds
	// it fails only that call — the model reads the error and can retry
	// with a narrower scope — so the number can be much smaller.
	//
	// Only the in-process wick provider reads this; CLI spawners ignore
	// it, since their tools run inside the agent's own scope already.
	ToolMemoryMaxMB int

	// ModelID pins which model this spawn should run, scoped to whichever
	// provider Instance is. Empty = that provider's own default-model
	// resolution (e.g. wick's pickModel Default/first-enabled behaviour).
	// Threaded from the session's AgentEntry.ModelID; providers that don't
	// support multiple models per instance ignore it.
	ModelID string
}
