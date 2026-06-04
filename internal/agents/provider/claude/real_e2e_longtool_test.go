package claude

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/state"
)

// longToolCase describes one long-running tool scenario.
type longToolCase struct {
	name string
	// prompt sent to claude (must produce a single bash tool call).
	prompt string
	// marker that must appear somewhere in the final response text.
	wantText string
	// how long the tool is expected to run (used to set IdleTimeout).
	toolDuration time.Duration
}

// longToolCases are the scenarios that exercise process stability under
// silent-stdout periods. Each tool call keeps stdout silent for the
// specified duration — the idle timer must NOT fire during that window.
//
// All cases require WICK_CLAUDE_E2E=1 and bypass mode so no human
// permission prompt blocks the bash tool.
var longToolCases = []longToolCase{
	{
		name:         "sleep_5m",
		prompt:       "Run this exact bash command and report what it printed: `sleep 300 && echo MARKER_SLEEP_5M`",
		wantText:     "MARKER_SLEEP_5M",
		toolDuration: 300 * time.Second,
	},
	{
		name: "slow_loop_5m",
		// A tight loop that prints progress every 30s — stdout is NOT
		// completely silent here. Tests that frequent but sparse output
		// keeps the idle timer alive across 5 minutes.
		prompt: `Run this exact bash command and report what it printed:
` + "`" + `for i in 1 2 3 4 5 6 7 8 9 10; do sleep 30 && echo TICK_$i; done && echo MARKER_LOOP_DONE` + "`",
		wantText:     "MARKER_LOOP_DONE",
		toolDuration: 320 * time.Second,
	},
	{
		name: "background_wait_5m",
		// Spawns a background process and waits — tests that claude
		// survives a wait-style tool call where there is no output until
		// the very end.
		prompt: "Run this exact bash command and report what it printed: `(sleep 305 && echo MARKER_BG_DONE) & wait`",
		wantText:     "MARKER_BG_DONE",
		toolDuration: 310 * time.Second,
	},
	{
		name: "multi_turn_after_long_tool",
		// Turn 1: long silent tool. Turn 2: immediate echo. Verifies that
		// the session is still usable (process alive + ResumeID valid)
		// after a >5 min silent tool run.
		prompt:       "Run this exact bash command and report what it printed: `sleep 300 && echo MARKER_MT_FIRST`",
		wantText:     "MARKER_MT_FIRST",
		toolDuration: 300 * time.Second,
	},
}

func TestRealClaudeLongToolStability(t *testing.T) {
	if !claudeE2EEnabled(t) {
		t.Skip("set WICK_CLAUDE_E2E=1 to run real-claude smoke tests")
	}
	for _, tc := range longToolCases {
		t.Run(tc.name, func(t *testing.T) {
			runLongToolCase(t, tc)
		})
	}
}

func runLongToolCase(t *testing.T, tc longToolCase) {
	t.Helper()

	// IdleTimeout = tool duration + 2 min buffer.
	idleTimeout := tc.toolDuration + 2*time.Minute
	// Test timeout = tool duration + 3 min response + buffer.
	testTimeout := tc.toolDuration + 4*time.Minute

	collected := &eventCollector{}
	st := state.New(nil)
	a := provider.New(provider.Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   idleTimeout,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       Spawner{BypassPermissions: true},
		State:         st,
		OnEvent:       collected.add,
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer a.Stop()

	t.Logf("[%s] sending prompt, tool expected to run ~%v", tc.name, tc.toolDuration)
	if err := a.Send(tc.prompt); err != nil {
		t.Fatalf("send: %v", err)
	}

	waitDeadline := testTimeout - 30*time.Second
	waitFor(t, func() bool {
		return collected.doneCount() >= 1 || collected.errorCount() >= 1
	}, waitDeadline)

	if n := collected.errorCount(); n > 0 {
		t.Fatalf("Error event fired (%d): %s", n, collected.firstError())
	}
	if collected.doneCount() < 1 {
		t.Fatal("Done never arrived")
	}
	if a.ResumeID() == "" {
		t.Fatal("ResumeID empty after long tool run")
	}

	turns := collected.textPerTurn()
	if len(turns) == 0 {
		t.Fatal("no text in any turn")
	}
	lastTurn := turns[len(turns)-1]
	if !containsCI(lastTurn, tc.wantText) {
		t.Errorf("response missing %q\ngot: %q", tc.wantText, lastTurn)
	}

	// multi_turn_after_long_tool: send a second turn to verify session alive.
	if tc.name == "multi_turn_after_long_tool" {
		t.Log("sending second turn to verify session still alive")
		if err := a.Send("Now run: `echo MARKER_MT_SECOND`"); err != nil {
			t.Fatalf("send turn 2: %v", err)
		}
		waitFor(t, func() bool {
			return collected.doneCount() >= 2 || collected.errorCount() >= 1
		}, 2*time.Minute)

		if n := collected.errorCount(); n > 0 {
			t.Fatalf("Error on turn 2: %s", collected.firstError())
		}
		if collected.doneCount() < 2 {
			t.Fatal("turn 2 Done never arrived")
		}
		turns2 := collected.textPerTurn()
		last2 := turns2[len(turns2)-1]
		if !containsCI(last2, "MARKER_MT_SECOND") {
			t.Errorf("turn 2 response missing MARKER_MT_SECOND\ngot: %q", last2)
		}
		t.Logf("session still alive after long tool: ResumeID=%s", a.ResumeID())
	}

	t.Logf("[%s] PASS — Done arrived, ResumeID=%s", tc.name, a.ResumeID())
}

// Compile-time check: fmt used for Sprintf in helpers elsewhere; keep
// the import from being dropped.
var _ = fmt.Sprintf
