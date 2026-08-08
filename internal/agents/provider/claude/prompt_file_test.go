package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The bug this file exists for: Windows caps a command line at 32767
// characters, and wick's preset alone is ~28KB. Inlining it spawned a
// process whose argv exceeded the limit, and CreateProcess reported
// "The filename or extension is too long" — naming the binary, saying
// nothing about length, so it read as a broken claude install.
//
// The argv must therefore stay small no matter how large the preset is.
func TestLargePresetDoesNotLandOnTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	preset := strings.Repeat("wick rules. ", 4000) // ~48KB, past the cap on its own

	args := systemPromptArgs(dir, "", preset)

	joined := strings.Join(args, " ")
	if len(joined) > 1000 {
		t.Fatalf("argv carries %d chars — a large preset must be referenced by path, not inlined", len(joined))
	}
	if strings.Contains(joined, "wick rules.") {
		t.Fatal("the preset text itself is on the command line")
	}
	if len(args) != 2 || args[0] != "--append-system-prompt-file" {
		t.Fatalf("args = %v, want --append-system-prompt-file <path>", args)
	}
}

// The prompt still has to REACH the agent. A file path that is written
// but never populated is worse than the original bug: the process starts,
// so nothing looks broken, and the agent runs with no wick rules, no
// session identity, and no role.
func TestPromptFileHoldsTheWholePreset(t *testing.T) {
	dir := t.TempDir()
	preset := "# rules\n\nsession_id: abc-123\n"

	args := systemPromptArgs(dir, "", preset)
	if len(args) != 2 {
		t.Fatalf("args = %v", args)
	}
	got, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if string(got) != preset {
		t.Fatalf("prompt file = %q, want the preset verbatim", got)
	}
}

// Under the SESSION dir, not the project workspace. The preset embeds
// the session identity block, and many sessions share one workspace — a
// workspace-local file is clobbered by whichever session spawned last
// and feeds the agent another session's id. Same rule codex's soul.md
// follows.
func TestPromptFileIsScopedToTheSession(t *testing.T) {
	sessionDir := t.TempDir()
	workspace := t.TempDir()

	args := systemPromptArgs(sessionDir, workspace, "prompt")
	if len(args) != 2 {
		t.Fatalf("args = %v", args)
	}
	if !strings.HasPrefix(args[1], sessionDir) {
		t.Fatalf("prompt file at %q, want it under the session dir %q", args[1], sessionDir)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".claude", promptFileName)); err == nil {
		t.Fatal("a prompt file was written into the shared workspace — it would clobber across sessions")
	}
}

// Two sessions sharing a workspace must not overwrite each other. This is
// the failure the scoping rule prevents, asserted directly rather than
// only through the path.
func TestTwoSessionsKeepTheirOwnPrompt(t *testing.T) {
	workspace := t.TempDir()
	a, b := t.TempDir(), t.TempDir()

	argsA := systemPromptArgs(a, workspace, "session A identity")
	argsB := systemPromptArgs(b, workspace, "session B identity")

	gotA, _ := os.ReadFile(argsA[1])
	if string(gotA) != "session A identity" {
		t.Fatalf("session A's prompt = %q — it was overwritten by another session", gotA)
	}
	gotB, _ := os.ReadFile(argsB[1])
	if string(gotB) != "session B identity" {
		t.Fatalf("session B's prompt = %q", gotB)
	}
}

// Falling back to inline is better than failing the spawn: a long argv
// MAY exceed the limit, while no prompt at all definitely breaks the
// agent's rules and identity.
func TestUnwritablePromptFallsBackToInline(t *testing.T) {
	// No session dir and no workspace: nowhere to write.
	args := systemPromptArgs("", "", "the preset")

	if len(args) != 2 || args[0] != "--append-system-prompt" {
		t.Fatalf("args = %v, want the inline flag when no file can be written", args)
	}
	if args[1] != "the preset" {
		t.Fatalf("inline value = %q, want the preset", args[1])
	}
}

// An empty preset must add nothing at all — an empty --append-system-prompt
// is not the same as omitting it.
func TestNoPresetAddsNoFlag(t *testing.T) {
	if args := systemPromptArgs(t.TempDir(), "", ""); len(args) != 0 {
		t.Fatalf("args = %v, want none", args)
	}
}

// Respawn (resume, idle-kill recovery) must not accumulate files or serve
// a stale prompt — the session block changes between spawns.
func TestRespawnOverwritesTheSamePromptFile(t *testing.T) {
	dir := t.TempDir()

	first := systemPromptArgs(dir, "", "first identity")
	second := systemPromptArgs(dir, "", "second identity")

	if first[1] != second[1] {
		t.Fatalf("respawn wrote a second file: %q then %q", first[1], second[1])
	}
	got, _ := os.ReadFile(second[1])
	if string(got) != "second identity" {
		t.Fatalf("prompt file = %q, want the latest preset", got)
	}
}

// Moving the prompt off the argv took it out of the process list, where
// any local account could read it. Writing it world-readable on disk
// would hand most of that back. Not a credential — the MCP token travels
// on --mcp-config — but it carries the operator's own prompt text and the
// session identity block.
//
// Unix only: Windows ACLs do not map onto the mode bits, and the
// temp-dir default there is already per-user.
func TestPromptFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	args := systemPromptArgs(dir, "", "session_id: abc-123")

	fi, err := os.Stat(args[1])
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("prompt file mode = %04o, want no group/other access", mode)
	}
}

// A sub-agent must identify itself to the MCP server by the id wick
// actually stored, or every op that resolves a delegation from its own
// session — progress, report_result — answers "this conversation is not
// a delegation".
//
// That is what happened: the id was taken from the basename of
// SessionDir, and a sub-agent's directory is NESTED
// (sessions/<parent>/subagents/<seg>) while its id is FLAT
// (<parent>--sub-<seg>). The basename is a bare segment matching no
// session, so supervision silently did nothing — the sub-agent dutifully
// called progress five times and was refused five times.
func TestSubAgentSendsItsRealSessionID(t *testing.T) {
	const childID = "18867658-110a-4853-83a9-e08237963548--sub-9f2c81ab40de"
	nestedDir := filepath.Join("sessions", "18867658-110a-4853-83a9-e08237963548", "subagents", "9f2c81ab40de")

	got := mcpSessionID(childID, nestedDir)

	if got != childID {
		t.Fatalf("session id = %q, want the flat child id %q — the MCP server cannot resolve the delegation otherwise", got, childID)
	}
	if got == "9f2c81ab40de" {
		t.Fatal("the id was derived from the directory basename again")
	}
}

// Callers that predate SessionID (tests, legacy paths) set only the dir.
// The basename is right for a top-level session, and no worse than an
// empty header for a child.
func TestSessionIDFallsBackToTheDirBasename(t *testing.T) {
	if got := mcpSessionID("", filepath.Join("sessions", "abc-123")); got != "abc-123" {
		t.Fatalf("fallback session id = %q, want abc-123", got)
	}
	if got := mcpSessionID("", ""); got != "" {
		t.Fatalf("with neither set, session id = %q, want empty", got)
	}
}
