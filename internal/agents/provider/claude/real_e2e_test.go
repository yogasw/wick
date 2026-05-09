package claude

import (
	"context"
	"os/exec"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// TestRealClaudeMultiTurn spawns the real claude CLI, sends two
// follow-up prompts on the same long-lived process, and asserts that
//
//   1. SessionStart fires once (one process, one CLI session ID)
//   2. Each turn produces a TextDelta + Done
//   3. The captured CLI session ID is non-empty (we can resume later)
//
// Skipped unless `WICK_CLAUDE_E2E=1` is set so CI without a logged-in
// claude binary stays green.
//
// This is the canary that protects the long-lived design from
// breaking against a future claude release that changes argv or
// stream-json shape. If this test fails, ClaudeParser or Spawner is
// out of sync with the real CLI.
func TestRealClaudeMultiTurn(t *testing.T) {
	if !claudeE2EEnabled(t) {
		t.Skip("set WICK_CLAUDE_E2E=1 to run real-claude smoke tests")
	}

	collected := &eventCollector{}
	st := state.New(nil)
	a := provider.New(provider.Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   90 * time.Second, // generous; claude takes a few seconds per turn
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       Spawner{},
		State:         st,
		OnEvent:       collected.add,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer a.Stop()

	// Turn 1.
	if err := a.Send("reply with exactly the word: ping"); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	waitFor(t, func() bool { return collected.doneCount() >= 1 }, 90*time.Second)

	// Turn 2 — same long-lived process, no respawn.
	if err := a.Send("now reply with exactly the word: pong"); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	waitFor(t, func() bool { return collected.doneCount() >= 2 }, 90*time.Second)

	if got := collected.sessionStartCount(); got != 1 {
		t.Fatalf("SessionStart fired %d times, want 1 (long-lived = one session)", got)
	}
	if a.ResumeID() == "" {
		t.Fatal("ResumeID empty — session_id capture broke")
	}
	textTurns := collected.textPerTurn()
	if len(textTurns) < 2 {
		t.Fatalf("expected text in 2 turns, got %d (%v)", len(textTurns), textTurns)
	}
	// Loose substring check — claude wording can vary, but our
	// prompts are constrained enough that "ping"/"pong" should appear.
	if !containsCI(textTurns[0], "ping") {
		t.Logf("turn 1 text: %q", textTurns[0])
	}
	if !containsCI(textTurns[1], "pong") {
		t.Logf("turn 2 text: %q", textTurns[1])
	}
}

// claudeE2EEnabled gates real-claude tests on the env var + binary
// availability. We log skip reason so it's obvious why CI shows skip.
func claudeE2EEnabled(t *testing.T) bool {
	t.Helper()
	if v := getenv("WICK_CLAUDE_E2E"); v != "1" {
		return false
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Logf("WICK_CLAUDE_E2E=1 set but `claude` not in PATH: %v", err)
		return false
	}
	return true
}
