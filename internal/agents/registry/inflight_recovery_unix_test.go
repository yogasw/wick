//go:build !windows

package registry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// holdTurnLock flocks the session's inflight.lock on its own descriptor,
// standing in for another process (the draining predecessor) running a
// turn there. The returned func lets go, as that process exiting would.
func holdTurnLock(t *testing.T, layout config.Layout, id string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(layout.SessionDir(id), "inflight.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = f.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// runningSession creates a session whose meta and agent say it is mid-turn.
func runningSession(t *testing.T, layout config.Layout, id string) {
	t.Helper()
	s, err := session.Create(context.Background(), layout, session.CreateOptions{ID: id, Origin: session.OriginUI})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, id, "main", "claude"); err != nil {
		t.Fatal(err)
	}
	s, _ = session.Load(layout, id)
	s.Meta.Status = session.StatusRunning
	s.Agents[0].Status = "working"
	_ = session.SaveMeta(layout, id, s.Meta)
	_ = session.SaveAgents(layout, id, s.Agents)
}

func convTurns(t *testing.T, layout config.Layout, id string) []store.ConversationTurn {
	t.Helper()
	var turns []store.ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation(id), func(line []byte) bool {
		var tr store.ConversationTurn
		if json.Unmarshal(line, &tr) == nil && tr.Role == "assistant" {
			turns = append(turns, tr)
		}
		return true
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return turns
}

func countInterrupted(turns []store.ConversationTurn) int {
	n := 0
	for _, tr := range turns {
		if tr.Interrupted {
			n++
		}
	}
	return n
}

// TestReloadSkipsSessionWithLiveTurn is the graceful-upgrade case: the
// predecessor still runs the turn, so the successor's boot must neither
// reset the session to idle nor fold its inflight.jsonl as interrupted.
// Once the turn ends normally, the deferred check settles it with no
// extra turn.
func TestReloadSkipsSessionWithLiveTurn(t *testing.T) {
	old := deferredRecoveryInterval
	deferredRecoveryInterval = time.Hour // the test drives recoverDeferred itself
	t.Cleanup(func() { deferredRecoveryInterval = old })

	layout := newLayout(t)
	runningSession(t, layout, "S1")
	// The predecessor's live turn: its own Store holds the turn lock on its
	// own descriptor, exactly as it would in the other process.
	live := store.New(store.Options{Layout: layout, SessionID: "S1", AgentName: "main"})
	if _, err := live.Apply(event.AgentEvent{Type: event.TextDelta, Text: "still working"}); err != nil {
		t.Fatal(err)
	}

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Session("S1")
	if got.Meta.Status != session.StatusRunning || got.Agents[0].Status != "working" {
		t.Fatalf("live session reset: meta=%q agent=%q", got.Meta.Status, got.Agents[0].Status)
	}
	disk, _ := session.Load(layout, "S1")
	if disk.Meta.Status != session.StatusRunning {
		t.Fatalf("disk status reset: %q", disk.Meta.Status)
	}
	if n := countInterrupted(convTurns(t, layout, "S1")); n != 0 {
		t.Fatalf("live turn recovered as interrupted (%d)", n)
	}
	if _, err := os.Stat(layout.SessionInflight("S1")); err != nil {
		t.Fatalf("inflight.jsonl of the live turn was consumed: %v", err)
	}
	if n := r.recoverDeferred(); n != 1 {
		t.Fatalf("still deferred = %d, want 1 while the turn runs", n)
	}

	// The turn finishes in the predecessor.
	if _, err := live.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
	if n := r.recoverDeferred(); n != 0 {
		t.Fatalf("still deferred = %d after the turn ended", n)
	}
	turns := convTurns(t, layout, "S1")
	if len(turns) != 1 || turns[0].Interrupted || turns[0].Text != "still working" {
		t.Fatalf("want one finished turn, got %+v", turns)
	}
}

// TestReloadDeferredRecoversWhenHolderDies: the predecessor was alive at
// boot but died mid-turn later. Once its lock frees, the deferred check
// does the recovery boot skipped.
func TestReloadDeferredRecoversWhenHolderDies(t *testing.T) {
	old := deferredRecoveryInterval
	deferredRecoveryInterval = time.Hour
	t.Cleanup(func() { deferredRecoveryInterval = old })

	layout := newLayout(t)
	runningSession(t, layout, "S1")
	die := holdTurnLock(t, layout, "S1")
	writeInflight(t, layout, "S1", "half an answer")

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if n := countInterrupted(convTurns(t, layout, "S1")); n != 0 {
		t.Fatalf("recovered while the holder lives (%d)", n)
	}

	die()
	if n := r.recoverDeferred(); n != 0 {
		t.Fatalf("still deferred = %d", n)
	}
	turns := convTurns(t, layout, "S1")
	if len(turns) != 1 || !turns[0].Interrupted || turns[0].Text != "half an answer" {
		t.Fatalf("want one recovered turn, got %+v", turns)
	}
	got, _ := r.Session("S1")
	if got.Meta.Status != session.StatusIdle || got.Agents[0].Status != "idle" {
		t.Fatalf("status not reset after holder died: meta=%q agent=%q", got.Meta.Status, got.Agents[0].Status)
	}
}

// TestReloadRecoversCrashedTurn: nobody holds the lock (the process that
// wrote inflight.jsonl crashed), so boot recovers it as before.
func TestReloadRecoversCrashedTurn(t *testing.T) {
	layout := newLayout(t)
	runningSession(t, layout, "S1")
	// A lock file left behind by the dead process, held by nobody.
	holdTurnLock(t, layout, "S1")()
	writeInflight(t, layout, "S1", "cut off")

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	turns := convTurns(t, layout, "S1")
	if len(turns) != 1 || !turns[0].Interrupted || !turns[0].Truncated || turns[0].Text != "cut off" {
		t.Fatalf("crashed turn not recovered: %+v", turns)
	}
	if _, err := os.Stat(layout.SessionInflight("S1")); !os.IsNotExist(err) {
		t.Fatalf("inflight.jsonl not removed: %v", err)
	}
	got, _ := r.Session("S1")
	if got.Meta.Status != session.StatusIdle || got.Agents[0].Status != "idle" {
		t.Fatalf("status not reset: meta=%q agent=%q", got.Meta.Status, got.Agents[0].Status)
	}
}

func writeInflight(t *testing.T, layout config.Layout, id, text string) {
	t.Helper()
	if err := storage.AppendJSONL(layout.SessionInflight(id), "wick-inflight-v1", id,
		store.InflightEntry{Type: "text_delta", Text: text, At: time.Now().UTC(), TurnID: "42"}); err != nil {
		t.Fatal(err)
	}
}
