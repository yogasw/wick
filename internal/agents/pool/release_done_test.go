package pool

import (
	"context"
	"testing"
	"time"
)

func sessionActive(p *Pool, sessionID string) (lifecycle string, ok bool) {
	for _, e := range p.ActiveSnapshot() {
		if e.SessionID == sessionID {
			return e.Lifecycle, true
		}
	}
	return "", false
}

// A session that answered is stopped at once, freeing its slot before the
// idle timeout would have.
func TestReleaseDoneStopsAnsweredSession(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{
		{`{"type":"system","subtype":"init","session_id":"x"}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`},
	}}
	p, layout := newPool(t, 2, sp)
	p.cfg.IdleTimeout = time.Minute
	setupSession(t, layout, "S-done")
	if err := p.Send(context.Background(), "S-done", "default", "team", "user", "task"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { lc, ok := sessionActive(p, "S-done"); return ok && lc == "idle" }, 3*time.Second)
	if n := p.ReleaseDone("S-done", "team task answered"); n != 1 {
		t.Fatalf("ReleaseDone = %d, want 1", n)
	}
	waitFor(t, func() bool { _, ok := sessionActive(p, "S-done"); return !ok }, 3*time.Second)
	if got := p.Capacity().Used; got != 0 {
		t.Fatalf("slots used after release = %d, want 0", got)
	}
}

// A session still in its turn is never stopped by ReleaseDone.
func TestReleaseDoneLeavesRunningTurn(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{}}} // never answers
	p, layout := newPool(t, 2, sp)
	p.cfg.IdleTimeout = time.Minute
	setupSession(t, layout, "S-busy")
	if err := p.Send(context.Background(), "S-busy", "default", "team", "user", "task"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, ok := sessionActive(p, "S-busy"); return ok }, 3*time.Second)
	if n := p.ReleaseDone("S-busy", "team task answered"); n != 0 {
		t.Fatalf("ReleaseDone on a running turn = %d, want 0", n)
	}
	if _, ok := sessionActive(p, "S-busy"); !ok {
		t.Fatal("running session was stopped")
	}
}

// A schedule's turn stops its process as soon as it ends; a person's turn
// in the same setup idles as usual.
func TestStopAfterMachineTurn(t *testing.T) {
	answer := []string{`{"type":"system","subtype":"init","session_id":"x"}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`}
	sp := &scriptedSpawner{Lines: [][]string{answer, answer}}
	p, layout := newPool(t, 2, sp)
	p.cfg.IdleTimeout = time.Minute
	p.cfg.StopAfterMachineTurn = func() bool { return true }
	setupSession(t, layout, "S-sched")
	setupSession(t, layout, "S-person")

	if err := p.Send(context.Background(), "S-sched", "default", "schedule", "user", "run"); err != nil {
		t.Fatal(err)
	}
	// Spawned, answered and released: the release can land before a poll
	// ever sees the process, so wait on the spawn count instead.
	waitFor(t, func() bool { sp.mu.Lock(); defer sp.mu.Unlock(); return sp.Calls >= 1 }, 3*time.Second)
	waitFor(t, func() bool { _, ok := sessionActive(p, "S-sched"); return !ok }, 5*time.Second)
	if got := p.Capacity().Used; got != 0 {
		t.Fatalf("slots used after the schedule's turn = %d, want 0", got)
	}

	if err := p.Send(context.Background(), "S-person", "default", "ui", "user", "hi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { lc, ok := sessionActive(p, "S-person"); return ok && lc == "idle" }, 3*time.Second)
	time.Sleep(time.Second)
	if _, ok := sessionActive(p, "S-person"); !ok {
		t.Fatal("a person's turn was stopped instead of idling")
	}
}
