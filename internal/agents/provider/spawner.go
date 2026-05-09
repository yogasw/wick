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
	// ExtraEnv lets the gate (phase 3) inject hook config paths
	// without coupling the agent package to gate internals.
	ExtraEnv []string
}
