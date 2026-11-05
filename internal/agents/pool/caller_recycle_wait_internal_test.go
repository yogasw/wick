package pool

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

func workingEntry(t *testing.T) *runEntry {
	t.Helper()
	m := state.New(nil)
	m.Apply(event.AgentEvent{Type: event.TextDelta, Text: "Hi Alex, "})
	if m.Lifecycle() != state.LifecycleWorking {
		t.Fatalf("lifecycle = %v, want working", m.Lifecycle())
	}
	return &runEntry{state: m, sessID: "s1"}
}

func shortRecycleWait(t *testing.T, wait time.Duration) {
	t.Helper()
	prevWait, prevPoll := callerRecycleWait, callerRecyclePoll
	callerRecycleWait, callerRecyclePoll = wait, 5*time.Millisecond
	t.Cleanup(func() { callerRecycleWait, callerRecyclePoll = prevWait, prevPoll })
}

// A new caller's message waits for the reply being written instead of
// killing the process halfway through it.
func TestCallerRecycleWaitsForTheTurnToEnd(t *testing.T) {
	shortRecycleWait(t, 5*time.Second)
	e := workingEntry(t)
	p := &Pool{active: map[string]*runEntry{"k": e}}
	go func() {
		time.Sleep(60 * time.Millisecond)
		e.state.Apply(event.AgentEvent{Type: event.Done})
	}()
	start := time.Now()
	p.waitTurnForRecycle(context.Background(), "k", e)
	if e.state.Lifecycle() == state.LifecycleWorking {
		t.Fatal("returned while the turn was still running")
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("did not wait for the turn")
	}
}

func TestCallerRecycleIdleProcessDoesNotWait(t *testing.T) {
	shortRecycleWait(t, 5*time.Second)
	m := state.New(nil)
	m.MarkIdle()
	e := &runEntry{state: m}
	p := &Pool{active: map[string]*runEntry{"k": e}}
	start := time.Now()
	p.waitTurnForRecycle(context.Background(), "k", e)
	if time.Since(start) > time.Second {
		t.Fatal("waited on an idle process")
	}
}

func TestCallerRecycleStopsWaitingWhenReplaced(t *testing.T) {
	shortRecycleWait(t, 5*time.Second)
	e := workingEntry(t)
	p := &Pool{active: map[string]*runEntry{"k": e}}
	go func() {
		time.Sleep(30 * time.Millisecond)
		p.mu.Lock()
		delete(p.active, "k")
		p.mu.Unlock()
	}()
	done := make(chan struct{})
	go func() { p.waitTurnForRecycle(context.Background(), "k", e); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("kept waiting on a process that is gone")
	}
}

func TestCallerRecycleGivesUpAfterTheCap(t *testing.T) {
	shortRecycleWait(t, 50*time.Millisecond)
	e := workingEntry(t)
	p := &Pool{active: map[string]*runEntry{"k": e}}
	done := make(chan struct{})
	go func() { p.waitTurnForRecycle(context.Background(), "k", e); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waited past the cap")
	}
}
