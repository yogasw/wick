package agents_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
)

// sessionFallbackCwd mirrors pool.resolveCwd's per-session temp-dir
// fallback. Tests that want to address per-session spawn scripts use
// this path because setupSess creates sessions without a workspace
// binding (see agents-design.md §0.2 D4).
func sessionFallbackCwd(layout config.Layout, id string) string {
	return filepath.Join(layout.SessionDir(id), "cwd")
}

// turn returns a 3-event canned turn (init + assistant text + result).
// Helper to keep scenario scripts readable.
func turn(sessionID, text string) turnScript {
	return turnScript{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"` + text + `"}`,
	}
}

// TestScenario_A_MultiTurnIdleKillResume verifies the core long-lived
// design: one session sends 3 messages on a single spawn, the idle TTL
// kills the process, the next message respawns with --resume <id> and
// claude conceptually continues the same conversation.
//
// Walks the pipeline:
//
//	send a → turn 1 → conversation.jsonl gets user+assistant
//	send b → turn 2 → conversation.jsonl gets user+assistant
//	send c → turn 3 → conversation.jsonl gets user+assistant
//	idle TTL fires → process killed, agents.json keeps cli_session_id
//	send d → respawn with ResumeID = previously captured id
//	         turn 1 of resume-spawn → conversation.jsonl appends turn 4
func TestScenario_A_MultiTurnIdleKillResume(t *testing.T) {
	sp := newMultiTurnSpawner()
	p, layout := newE2EPool(t, 2, sp)
	setupSess(t, layout, "A")

	workspace := sessionFallbackCwd(layout,"A")
	sp.SetTurns(workspace,
		// First spawn: 3 turns, then process exits when stdin closes.
		[]turnScript{
			turn("sess-A", "answer-a"),
			turn("sess-A", "answer-b"),
			turn("sess-A", "answer-c"),
		},
		// Second spawn (after idle kill + resume): 1 turn.
		[]turnScript{
			turn("sess-A", "answer-d-resumed"),
		},
	)

	ctx := context.Background()
	for _, msg := range []string{"ask-a", "ask-b", "ask-c"} {
		if err := p.Send(ctx, "A", "default", "ui", "user", msg); err != nil {
			t.Fatalf("send %q: %v", msg, err)
		}
		// Each Send must complete before the next so per-turn assertions
		// stay deterministic. Wait for store.Apply to flush this turn's
		// assistant entry into conversation.jsonl.
		waitForTurns(t, layout, "A", expectedTurnCount(msg))
	}

	// Idle TTL is 200ms (newE2EPool config). Wait for the active count
	// to drop — the agent kills its own subprocess when no events
	// arrive within the TTL window.
	waitFor(t, func() bool { return p.Active() == 0 }, 3*time.Second)

	sess, _ := session.Load(layout, "A")
	if sess.Agents[0].CLISessionID != "sess-A" {
		t.Fatalf("cli_session_id not persisted before idle kill: %q", sess.Agents[0].CLISessionID)
	}

	// Now send the 4th message — pool should respawn with ResumeID set.
	if err := p.Send(ctx, "A", "default", "ui", "user", "ask-d"); err != nil {
		t.Fatal(err)
	}
	waitForTurns(t, layout, "A", 8) // 4 user + 4 assistant turns total
	waitFor(t, func() bool { return p.Active() == 0 }, 3*time.Second)

	// Verify the second spawn was invoked with --resume <captured-id>.
	spawns := sp.SpawnsFor(workspace)
	if len(spawns) != 2 {
		t.Fatalf("spawn count: got %d, want 2 (initial + resume)", len(spawns))
	}
	if spawns[0].ResumeID != "" {
		t.Fatalf("first spawn ResumeID should be empty, got %q", spawns[0].ResumeID)
	}
	if spawns[1].ResumeID != "sess-A" {
		t.Fatalf("resume spawn ResumeID: got %q, want sess-A", spawns[1].ResumeID)
	}

	turns := readTurns(t, layout, "A")
	if len(turns) != 8 {
		t.Fatalf("conversation turns: got %d, want 8 (4× user + 4× assistant)", len(turns))
	}
	// Check assistant texts in order.
	wantAssistant := []string{"answer-a", "answer-b", "answer-c", "answer-d-resumed"}
	got := []string{}
	for _, ttn := range turns {
		if ttn.Role == "assistant" {
			got = append(got, ttn.Text)
		}
	}
	for i, w := range wantAssistant {
		if got[i] != w {
			t.Fatalf("assistant turn %d: got %q want %q", i, got[i], w)
		}
	}
}

// TestScenario_B_MultiTurnExplicitStop drives 4 turns on one spawn,
// then calls pool.Stop() to mimic graceful shutdown. Verifies all 4
// turns landed in conversation.jsonl before stop and the agent
// teardown is clean (no panic, status returns to idle on disk).
func TestScenario_B_MultiTurnExplicitStop(t *testing.T) {
	sp := newMultiTurnSpawner()
	p, layout := newE2EPool(t, 2, sp)
	setupSess(t, layout, "B")

	workspace := sessionFallbackCwd(layout,"B")
	sp.SetTurns(workspace, []turnScript{
		turn("sess-B", "rep-a"),
		turn("sess-B", "rep-b"),
		turn("sess-B", "rep-c"),
		turn("sess-B", "rep-d"),
	})

	ctx := context.Background()
	for i, msg := range []string{"q-a", "q-b", "q-c", "q-d"} {
		if err := p.Send(ctx, "B", "default", "ui", "user", msg); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		waitForTurns(t, layout, "B", (i+1)*2)
	}

	p.Stop()
	waitFor(t, func() bool { return p.Active() == 0 }, 3*time.Second)

	turns := readTurns(t, layout, "B")
	if len(turns) != 8 {
		t.Fatalf("turns: got %d, want 8", len(turns))
	}
	sess, _ := session.Load(layout, "B")
	if sess.Meta.Status != session.StatusIdle {
		t.Fatalf("session status after stop: %q, want idle", sess.Meta.Status)
	}
}

// TestScenario_ConcurrentSessionsQueueDrains is the headliner: three
// sessions A, B, C with pool max=2.
//
//	A: 3 turns → eventually idle, slot frees
//	B: 4 turns → keeps slot occupied longer
//	C: arrives while pool is full → status=queued, message buffered
//	   When A's slot frees, C is granted, spawns, drains buffer.
//
// Verifies:
//   - C goes through queued → running → idle
//   - C's pending_input is drained (cleared after spawn)
//   - All three sessions end up with their full conversation logs
//   - Total spawn count matches expectations (1 per session here,
//     because none idle-kill within the test window)
func TestScenario_ConcurrentSessionsQueueDrains(t *testing.T) {
	sp := newMultiTurnSpawner()
	p, layout := newE2EPool(t, 2, sp)
	for _, id := range []string{"A", "B", "C"} {
		setupSess(t, layout, id)
	}

	sp.SetTurns(sessionFallbackCwd(layout,"A"), []turnScript{
		turn("sess-A", "A1"),
		turn("sess-A", "A2"),
		turn("sess-A", "A3"),
	})
	sp.SetTurns(sessionFallbackCwd(layout,"B"), []turnScript{
		turn("sess-B", "B1"),
		turn("sess-B", "B2"),
		turn("sess-B", "B3"),
		turn("sess-B", "B4"),
	})
	sp.SetTurns(sessionFallbackCwd(layout,"C"), []turnScript{
		turn("sess-C", "C1"),
	})

	ctx := context.Background()

	// Fill both slots: A and B.
	if err := p.Send(ctx, "A", "default", "ui", "user", "a-msg-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(ctx, "B", "default", "ui", "user", "b-msg-1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 2 }, 3*time.Second)

	// C tries to send → pool full → must queue, not spawn.
	if err := p.Send(ctx, "C", "default", "ui", "user", "c-msg-1"); err != nil {
		t.Fatal(err)
	}
	if p.QueueLen() != 1 {
		t.Fatalf("queue len after C send: got %d, want 1", p.QueueLen())
	}
	sessC, _ := session.Load(layout, "C")
	if sessC.Meta.Status != session.StatusQueued {
		t.Fatalf("C status: %q, want queued", sessC.Meta.Status)
	}
	if len(sessC.Meta.PendingInput) != 1 || sessC.Meta.PendingInput[0] != "c-msg-1" {
		t.Fatalf("C pending_input: %v", sessC.Meta.PendingInput)
	}
	if cSpawns := sp.SpawnsFor(sessionFallbackCwd(layout,"C")); len(cSpawns) != 0 {
		t.Fatalf("C spawned while pool was full: %v", cSpawns)
	}

	// Drive A through its 3 turns; afterwards A's idle TTL kicks in
	// (200ms in newE2EPool), the slot frees, and C should be granted.
	for _, msg := range []string{"a-msg-2", "a-msg-3"} {
		if err := p.Send(ctx, "A", "default", "ui", "user", msg); err != nil {
			t.Fatalf("A %q: %v", msg, err)
		}
	}
	// Drive B through 4 turns.
	for _, msg := range []string{"b-msg-2", "b-msg-3", "b-msg-4"} {
		if err := p.Send(ctx, "B", "default", "ui", "user", msg); err != nil {
			t.Fatalf("B %q: %v", msg, err)
		}
	}

	// Wait for C to be granted a slot, spawn, complete its 1 turn,
	// and idle-kill. The full chain takes a few hundred ms.
	waitFor(t, func() bool {
		s, _ := session.Load(layout, "C")
		return s.Meta.Status == session.StatusIdle && len(s.Meta.PendingInput) == 0
	}, 5*time.Second)

	waitFor(t, func() bool { return p.Active() == 0 && p.QueueLen() == 0 }, 5*time.Second)

	// Each session should have spawned exactly once (no respawn within
	// this short test) — except possibly A or B if the test was slow.
	// Lower bound: 3 spawns total.
	allSpawns := sp.SpawnLog()
	if len(allSpawns) < 3 {
		t.Fatalf("spawn count: got %d, want at least 3 (one per session)", len(allSpawns))
	}

	// Conversation logs should be complete for each session.
	checkConv := func(id string, wantAssistant []string) {
		t.Helper()
		turns := readTurns(t, layout, id)
		var got []string
		for _, ttn := range turns {
			if ttn.Role == "assistant" {
				got = append(got, ttn.Text)
			}
		}
		if len(got) != len(wantAssistant) {
			t.Fatalf("session %s assistant count: got %d (%v), want %d (%v)",
				id, len(got), got, len(wantAssistant), wantAssistant)
		}
		for i, w := range wantAssistant {
			if got[i] != w {
				t.Fatalf("session %s assistant[%d]: got %q want %q", id, i, got[i], w)
			}
		}
	}
	checkConv("A", []string{"A1", "A2", "A3"})
	checkConv("B", []string{"B1", "B2", "B3", "B4"})
	checkConv("C", []string{"C1"})

	// CLI session IDs persisted for each.
	for _, id := range []string{"A", "B", "C"} {
		s, _ := session.Load(layout, id)
		if s.Agents[0].CLISessionID == "" {
			t.Fatalf("session %s cli_session_id empty", id)
		}
	}
}

// expectedTurnCount returns how many conversation.jsonl entries we
// expect after the given user message. Each Send writes 1 user +
// 1 assistant turn = 2 entries, indexed by the message label suffix.
func expectedTurnCount(msg string) int {
	switch msg {
	case "ask-a":
		return 2
	case "ask-b":
		return 4
	case "ask-c":
		return 6
	}
	// Fallback for free-form messages — assume next pair.
	return 2
}

// waitForTurns polls conversation.jsonl until the entry count reaches
// `want` or the timeout fires. Used because Apply → AppendJSONL is
// async w.r.t. the Send return value.
func waitForTurns(t *testing.T, layout config.Layout, sessionID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(readTurns(t, layout, sessionID)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s: timed out waiting for %d turns, got %d", sessionID, want, len(readTurns(t, layout, sessionID)))
}

// _ stops "unused" complaints if pool is only referenced via newE2EPool.
var _ = pool.Pool{}
var _ = strings.Contains
