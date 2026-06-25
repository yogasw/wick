package pool

import (
	"context"
	"testing"
	"time"
)

// TestSystemTurnDoesNotSpawnAlone is the regression for the "agent replies
// twice" bug: channels inject a one-time origin-context turn (role=system)
// right before the first user message. The context turn must NOT spawn the
// agent on its own — otherwise the agent runs against just the context
// ("I don't see a request") and runs again when the user turn lands. The
// user turn that follows spawns once and Drain combines both.
func TestSystemTurnDoesNotSpawnAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	// 1. Context turn alone — must not spawn.
	if err := p.Send(context.Background(), "S1", "default", "slack", "system", "[thread context] room=42"); err != nil {
		t.Fatal(err)
	}
	// Give any erroneous spawn a chance to fire.
	time.Sleep(200 * time.Millisecond)
	if n := sp.procCount(); n != 0 {
		t.Fatalf("system turn spawned the agent (%d procs); it must wait for the user turn", n)
	}

	// 2. User turn — now spawn exactly once, with BOTH context and message.
	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "bantu cek cpp-japfa"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "thread context") || !contains(stdin, "bantu cek cpp-japfa") {
		t.Fatalf("combined prompt missing context or message: %q", stdin)
	}
}
