package claude

import (
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

// Passing the system prompt as an argument does not scale.
//
// Windows caps a process command line at 32767 UTF-16 characters, and
// CreateProcess refuses anything longer with "The filename or extension
// is too long" — a message that names the binary and says nothing about
// length, so the failure reads as a missing/corrupt claude install. It is
// not: the binary is fine and the argv is oversized.
//
// wick's preset is ~28KB before a user's own prompt, a project addon, or
// a sub-agent role is added, so the ceiling is not a distant edge case —
// it is reached by the default configuration plus a couple of --add-dir
// entries. Every spawn on Windows then fails, and the session shows a
// system error turn instead of an agent.
//
// So the prompt goes in a file and only its PATH goes on the argv. Same
// move codex already makes with model_instructions_file (see
// codex/spawn.go), for a different reason there and the same one here.

// promptFileName is the per-session file holding the rendered preset.
const promptFileName = "system-prompt.md"

// writePromptFile stores the preset next to the session and returns the
// path to pass to --append-system-prompt-file.
//
// It lives under the SESSION dir rather than the project workspace for
// the reason codex's soul.md does: the preset embeds the session identity
// block (session_id, channel, title), and many sessions can share one
// project workspace. A workspace-local file would be clobbered by
// whichever session spawned last and hand the agent another session's id.
//
// Returns "" when there is nowhere safe to write, which is the caller's
// signal to fall back to passing the prompt inline. Failing the spawn
// instead would trade a working-but-long argv for no agent at all.
func writePromptFile(sessionDir, workspace, preset string) string {
	if preset == "" {
		return ""
	}
	dir := sessionDir
	if dir == "" {
		// Legacy and test paths have no session dir. The workspace is a
		// worse home for this file (see above) but still better than an
		// argv that may not spawn.
		dir = workspace
	}
	if dir == "" {
		return ""
	}
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		log.Warn().Err(err).Str("dir", claudeDir).
			Msg("agents.spawn: cannot create prompt dir; passing the system prompt inline")
		return ""
	}
	p := filepath.Join(claudeDir, promptFileName)
	// 0600, not 0644. The preset is not a credential — the MCP token
	// travels on --mcp-config, not in here — but it does carry the
	// operator's own prompt text and the session identity block, and on a
	// shared machine there is no reason for another account to read
	// either. Moving it off the argv already took it out of the process
	// list; leaving it world-readable on disk would give most of that back.
	if err := os.WriteFile(p, []byte(preset), 0o600); err != nil {
		log.Warn().Err(err).Str("path", p).
			Msg("agents.spawn: write system prompt file failed; passing it inline")
		return ""
	}
	return p
}

// systemPromptArgs decides how the preset reaches claude.
//
// Prefers the file. Falls back to inline when the file could not be
// written, and says so in the log rather than silently dropping the
// prompt: an agent spawned without its preset has no wick rules, no
// session identity, and no role — it looks like the model "forgetting"
// rather than a write failing.
//
// --append-system-prompt-file is NOT listed as its own entry in
// `claude --help` (only referenced in prose under other flags), so
// support cannot be probed the way --mcp-config is. It is verified
// present on 2.1.220; older binaries that reject it surface a clear
// startup error naming the flag, which is why the inline path stays.
func systemPromptArgs(sessionDir, workspace, preset string) []string {
	if preset == "" {
		return nil
	}
	if path := writePromptFile(sessionDir, workspace, preset); path != "" {
		return []string{"--append-system-prompt-file", path}
	}
	log.Warn().Int("preset_bytes", len(preset)).
		Msg("agents.spawn: passing the system prompt on the command line; on Windows a large prompt can exceed the 32767-character limit")
	return []string{"--append-system-prompt", preset}
}
