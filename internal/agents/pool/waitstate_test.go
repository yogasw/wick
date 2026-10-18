package pool

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// An idle kill the pool retries shows as "restarting, attempt N of 2"
// while it runs; once the retries are spent the session reads nothing,
// and the exit's Done gets the plain reason once.
func TestIdleRetryWaitStateThenGiveUp(t *testing.T) {
	old := idleRetryBackoff
	idleRetryBackoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { idleRetryBackoff = old })
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sp := &silentSpawner{}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}
	p := New(PoolConfig{
		MaxConcurrent: 1, IdleTimeout: 150 * time.Millisecond, TurnIdleTimeout: 150 * time.Millisecond,
		Layout: layout, Factory: factory,
	})
	var mu sync.Mutex
	seen := map[int]bool{}
	p.SetOnWaitChange(func() {
		w := p.WaitOf("S1")
		mu.Lock()
		seen[w.RestartAttempt] = true
		mu.Unlock()
	})
	var gaveUp string
	factory.OnExit = func(sid, name string, reason provider.ExitReason, detail string) {
		_, restarting := p.HandleExitOutcome(sid, name, reason, detail)
		if !restarting {
			if r := p.TakeGiveUp(sid); r != "" {
				mu.Lock()
				gaveUp = r
				mu.Unlock()
			}
		}
	}
	t.Cleanup(p.Stop)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "main", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "are you there"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return gaveUp != ""
	}, 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	if !seen[1] || !seen[2] {
		t.Fatalf("restart attempts seen = %v, want 1 and 2", seen)
	}
	if want := "stopped after 2 retries: no output for 150ms"; gaveUp != want {
		t.Fatalf("give-up reason = %q, want %q", gaveUp, want)
	}
	if w := p.WaitOf("S1"); w.RestartAttempt != 0 {
		t.Fatalf("still restarting after give-up: %+v", w)
	}
	if r := p.TakeGiveUp("S1"); r != "" {
		t.Fatalf("give-up reason read twice: %q", r)
	}
}

// Output from the restarted agent ends its restarting state.
func TestNoteOutputClearsRestarting(t *testing.T) {
	p := &Pool{}
	var calls int
	var mu sync.Mutex
	done := make(chan struct{}, 4)
	p.SetOnWaitChange(func() { mu.Lock(); calls++; mu.Unlock(); done <- struct{}{} })
	p.markRestarting("S", 1)
	<-done
	if w := p.WaitOf("S"); w.RestartAttempt != 1 || w.RestartMax != maxIdleRetries {
		t.Fatalf("WaitOf = %+v", w)
	}
	if n := p.WaitOf("S").Note(); n != "target restarting after an idle timeout, retry 1/2" {
		t.Fatalf("Note = %q", n)
	}
	p.NoteOutput("S")
	<-done
	if w := p.WaitOf("S"); w != (WaitState{}) {
		t.Fatalf("after output: %+v", w)
	}
	p.NoteOutput("S") // nothing restarting: no change
	select {
	case <-done:
		t.Fatal("NoteOutput without a restart fired a change")
	case <-time.After(30 * time.Millisecond):
	}
}

// A queued session reads its place in the queue.
func TestWaitOfQueuePos(t *testing.T) {
	p := &Pool{queue: []queueEntry{{"A", "main", time.Now()}, {"B", "main", time.Now()}}}
	if w := p.WaitOf("B"); w.QueuePos != 2 {
		t.Fatalf("B = %+v", w)
	}
	if n := p.WaitOf("A").Note(); n != "target queued for a free slot (#1)" {
		t.Fatalf("A note = %q", n)
	}
	if w := p.WaitOf("C"); w != (WaitState{}) {
		t.Fatalf("C = %+v", w)
	}
}

func TestIdleGiveUpReason(t *testing.T) {
	cases := map[time.Duration]string{
		20 * time.Minute: "stopped after 2 retries: no output for 20m",
		2 * time.Minute:  "stopped after 2 retries: no output for 2m",
		90 * time.Second: "stopped after 2 retries: no output for 1m30s",
		30 * time.Second: "stopped after 2 retries: no output for 30s",
		time.Hour:        "stopped after 2 retries: no output for 1h",
	}
	for d, want := range cases {
		if got := idleGiveUpReason(2, d, false); got != want {
			t.Errorf("%s: got %q, want %q", d, got, want)
		}
	}
	if got := idleGiveUpReason(2, time.Minute, true); got == "" || got == idleGiveUpReason(2, time.Minute, false) {
		t.Errorf("halted reason = %q", got)
	}
}
