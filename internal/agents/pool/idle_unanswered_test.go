package pool

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// A message drained into a spawn that never wrote a line before the idle
// timer killed it goes back to the buffer and the agent is started again on
// its own, maxIdleRetries times. Only then is the person told inline that
// the message waits for the next one. Before, the spawn had already drained
// it: it died with the process, and nothing ever woke the agent again.
func TestIdleKillBeforeAnyOutputRetriesThenRebuffers(t *testing.T) {
	old := idleRetryBackoff
	idleRetryBackoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { idleRetryBackoff = old })
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sp := &silentSpawner{}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}
	var mu sync.Mutex
	var notices []string
	p := New(PoolConfig{
		MaxConcurrent: 1,
		IdleTimeout:   150 * time.Millisecond,
		// Not above IdleTimeout: an open turn gets no longer window, so a
		// silent spawn is killed on the idle window as before.
		TurnIdleTimeout: 150 * time.Millisecond,
		Layout:          layout,
		Factory:         factory,
		OnSpawnError: func(ev SpawnErrorEvent) {
			mu.Lock()
			notices = append(notices, ev.Message)
			mu.Unlock()
		},
	})
	factory.OnExit = p.HandleExit
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
	// The first spawn plus one per retry, without anyone writing again.
	waitFor(t, func() bool { return sp.callsSnapshot() == 1+maxIdleRetries }, 5*time.Second)

	pending := func() []string {
		sess, err := session.Load(layout, "S1")
		if err != nil {
			t.Fatal(err)
		}
		return sess.Meta.PendingInput
	}
	noticeCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(notices)
	}
	// The retries are spent: the give-up notice is announced once, inline.
	waitFor(t, func() bool { return noticeCount() >= 1 }, 3*time.Second)
	got := pending()
	if len(got) != 2 {
		t.Fatalf("pending after give-up: %q", got)
	}
	if !strings.Contains(got[0], "are you there") || !strings.Contains(got[0], "Retrying automatically") {
		t.Fatalf("message (with the retry notice) not re-buffered first: %q", got)
	}
	if !strings.Contains(got[1], "automatic retry did not get an answer") {
		t.Fatalf("give-up notice not buffered after it: %q", got)
	}
	// The give-up notice does not spawn on its own.
	time.Sleep(300 * time.Millisecond)
	if c := sp.callsSnapshot(); c != 1+maxIdleRetries {
		t.Fatalf("spawns after giving up: %d, want %d", c, 1+maxIdleRetries)
	}
	if n := noticeCount(); n != 1 {
		t.Fatalf("inline notices: %d, want 1", n)
	}

	// The next message wakes the agent and carries the kept one with it.
	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "hello?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() == 2+maxIdleRetries }, 2*time.Second)
	waitFor(t, func() bool {
		got := pending()
		return len(got) >= 1 && strings.Contains(got[0], "hello?")
	}, 3*time.Second)
	if got := pending(); !strings.Contains(got[0], "are you there") {
		t.Fatalf("next spawn did not carry the kept message: %q", got)
	}
}

// Crash recovery that already gave up on an agent is not undone by an
// idle retry: a process that cannot start would only loop.
func TestIdleRetrySkippedWhenCrashRecoveryHalted(t *testing.T) {
	p := New(PoolConfig{})
	t.Cleanup(p.Stop)
	key := sessionKey("S1", "main")
	p.mu.Lock()
	noteAttemptLocked(&p.crashes, key, time.Now())
	p.markHaltedLocked(key)
	p.mu.Unlock()
	if p.retryUnanswered("S1", "main", 1) {
		t.Fatal("retry scheduled for a halted agent")
	}
}

// The retry budget is per window: two retries, then none until the window
// passes.
func TestIdleRetryBudget(t *testing.T) {
	old := idleRetryBackoff
	idleRetryBackoff = []time.Duration{time.Hour}
	t.Cleanup(func() { idleRetryBackoff = old })
	p := New(PoolConfig{})
	t.Cleanup(p.Stop)
	for i := 1; i <= maxIdleRetries; i++ {
		if !p.retryUnanswered("S1", "main", 1) {
			t.Fatalf("retry %d not scheduled", i)
		}
	}
	if p.retryUnanswered("S1", "main", 1) {
		t.Fatal("retry scheduled past the budget")
	}
	p.mu.Lock()
	p.idleRetries[sessionKey("S1", "main")].first = time.Now().Add(-2 * respawnWindow)
	p.mu.Unlock()
	if !p.retryUnanswered("S1", "main", 1) {
		t.Fatal("budget not renewed after the window")
	}
}

func TestIdleRetryNotice(t *testing.T) {
	if got := idleRetryNotice(1, 1); !strings.Contains(got, "Your last message was") || !strings.Contains(got, "attempt 1 of 2") {
		t.Fatalf("notice: %q", got)
	}
	if got := idleRetryNotice(3, 2); !strings.Contains(got, "Your last 3 messages were") || !strings.Contains(got, "attempt 2 of 2") {
		t.Fatalf("notice: %q", got)
	}
}
