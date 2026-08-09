package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
)

// A spawn that arrives WITHOUT a prior Send has no in-memory buffer: a
// leader woken by a delegation result, a preempt-requeue. That exact spawn
// used to leave entry.buffer nil and panic in Drain — inside the bare
// queue-grant goroutine (tryGrantQueue), so the nil took the whole process
// down. Observed in the wild during a sub-agent test run.
func TestSpawnWithoutPriorSendDoesNotPanic(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "WAKE")

	if err := p.spawn(context.Background(), "WAKE", "default", "queue"); err != nil {
		t.Fatalf("spawn without prior send: %v", err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)
}

// The semantic half of the same fix: bufferFor reads PendingInput persisted
// on disk, so input queued before a crash/restart is delivered by the next
// spawn even when no in-memory buffer ever existed.
func TestSpawnWithoutBufferDrainsPendingInputFromDisk(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "WAKE")

	sess, err := session.Load(layout, "WAKE")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.PendingInput = []string{"queued before restart"}
	if err := session.SaveMeta(layout, "WAKE", sess.Meta); err != nil {
		t.Fatal(err)
	}

	if err := p.spawn(context.Background(), "WAKE", "default", "queue"); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sp.mu.Lock()
	last := sp.Last
	sp.mu.Unlock()
	if got := last.recordedStdin(); !strings.Contains(got, "queued before restart") {
		t.Fatalf("pending input from disk was not drained into the spawn; stdin:\n%s", got)
	}
}

// Backstop for the next wiring gap: Drain on a nil buffer must be a no-op,
// never a process-killing nil deref in a bare goroutine.
func TestDrainNilBufferIsSafe(t *testing.T) {
	var b *Buffer
	out, err := b.Drain()
	if err != nil || out != "" {
		t.Fatalf("nil Drain = (%q, %v), want (\"\", nil)", out, err)
	}
}
