package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/state"
)

// TestRealClaudePartialStreaming spawns claude with the default spawner
// (which passes --include-partial-messages) and asserts that a single
// turn produces MORE THAN ONE TextDelta event — i.e. the assistant text
// arrives as a stream of chunks, not one batched blob from the trailing
// `assistant` frame.
//
// Without --include-partial-messages, a short claude reply emits exactly
// one TextDelta (the concatenated text from the final `assistant` frame).
// With the flag, the API streams content_block_delta events as the
// model generates, and the parser turns each into its own TextDelta.
//
// Skipped unless WICK_CLAUDE_E2E=1 + `claude` on PATH (same gate as the
// other real-claude tests).
func TestRealClaudePartialStreaming(t *testing.T) {
	if !claudeE2EEnabled(t) {
		t.Skip("set WICK_CLAUDE_E2E=1 to run real-claude smoke tests")
	}

	collected := &eventCollector{}
	st := state.New(nil)
	a := provider.New(provider.Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   90 * time.Second,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       Spawner{BypassPermissions: true},
		State:         st,
		OnEvent:       collected.add,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer a.Stop()

	// Ask for a long reply so claude has enough output to span several
	// content_block_delta frames. Short answers may fit in one delta
	// even with the flag on, which would falsely fail the test.
	if err := a.Send("write a 200-word paragraph about the history of the internet — keep going to roughly 200 words, no lists, no headings"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, func() bool { return collected.doneCount() >= 1 }, 90*time.Second)

	if collected.errorCount() > 0 {
		t.Fatalf("claude returned an error: %s", collected.firstError())
	}

	deltas := collected.textDeltaCount()
	turns := collected.textPerTurn()
	if len(turns) == 0 || turns[0] == "" {
		t.Fatalf("turn text empty; raw events=%d, deltas=%d", len(collected.events), deltas)
	}

	// Primary assertion: multiple chunks. A long reply *must* arrive in
	// pieces when --include-partial-messages is on — otherwise the flag
	// is silently ineffective and the UI gets one blob at the end.
	if deltas < 2 {
		t.Fatalf("expected multiple TextDelta chunks from --include-partial-messages (long reply should stream), got %d for %d-char body: %q",
			deltas, len(turns[0]), turns[0])
	}

	t.Logf("partial-streaming verified: %d TextDelta chunks reassembled into %d-char body", deltas, len(turns[0]))

	// Sanity: dedup logic didn't lose the body. "internet" should
	// survive since the prompt asks about it.
	if !strings.Contains(strings.ToLower(turns[0]), "internet") {
		t.Fatalf("turn body lost expected content — dedup may be wrong: %q", turns[0])
	}
}
