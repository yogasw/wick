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
// timer killed it goes back to the buffer, and the person is told inline.
// Before, the spawn had already drained it: it died with the process, the
// next spawn ran without it, and the chat showed no reply and no event.
func TestIdleKillBeforeAnyOutputRebuffersMessages(t *testing.T) {
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
		Layout:        layout,
		Factory:       factory,
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
	waitFor(t, func() bool { return sp.callsSnapshot() == 1 }, 2*time.Second)

	pending := func() []string {
		sess, err := session.Load(layout, "S1")
		if err != nil {
			t.Fatal(err)
		}
		return sess.Meta.PendingInput
	}
	waitFor(t, func() bool { return len(pending()) == 2 }, 3*time.Second)
	got := pending()
	if !strings.Contains(got[0], "are you there") {
		t.Fatalf("message not re-buffered first: %q", got)
	}
	if !strings.Contains(got[1], "not answered") {
		t.Fatalf("notice not buffered after it: %q", got)
	}
	mu.Lock()
	n := len(notices)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("inline notices: %d, want 1", n)
	}
	// The notice does not spawn on its own.
	if c := sp.callsSnapshot(); c != 1 {
		t.Fatalf("spawns after idle kill: %d, want 1", c)
	}

	// The next message wakes the agent and carries the kept one with it.
	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "hello?"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() == 2 }, 2*time.Second)
	// That spawn is silent too: what comes back is the one combined input
	// it was given, so the kept message did reach it.
	waitFor(t, func() bool {
		got := pending()
		return len(got) == 2 && strings.Contains(got[0], "hello?")
	}, 3*time.Second)
	if got := pending(); !strings.Contains(got[0], "are you there") {
		t.Fatalf("second spawn did not carry the kept message: %q", got)
	}
}
