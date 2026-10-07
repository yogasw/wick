package pool

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// Respawn-per-turn providers (opencode, codex, omp) spawn on the first Send,
// not at Start. A refused spawn there — opencode with no model, a missing
// binary — must read the same way a failed Start does: inline error turn,
// OnSpawnError, slot released, session idle. Before, the error went back to
// the HTTP caller only and the session sat "running" with no process.
func TestFirstSendSpawnFailureOnRespawnProviderSurfacesInline(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sp := &failingSpawner{err: errors.New("opencode instance oc: log in first: no model")}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}

	var (
		mu     sync.Mutex
		gotMsg string
	)
	p := New(PoolConfig{
		MaxConcurrent: 2,
		IdleTimeout:   500 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
		OnSpawnError: func(ev SpawnErrorEvent) {
			mu.Lock()
			gotMsg = ev.Message
			mu.Unlock()
		},
	})
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "main", "opencode"); err != nil {
		t.Fatal(err)
	}

	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "hello"); err != nil {
		t.Fatalf("spawn failure should be surfaced inline, not returned: %v", err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	mu.Lock()
	msg := gotMsg
	mu.Unlock()
	if !strings.Contains(msg, "no model") || !strings.HasPrefix(msg, "Failed to start ") {
		t.Fatalf("OnSpawnError message = %q", msg)
	}
	sess, err := session.Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.Status != session.StatusIdle {
		t.Fatalf("status = %q, want idle", sess.Meta.Status)
	}
	data, err := os.ReadFile(layout.SessionConversation("S1"))
	if err != nil {
		t.Fatal(err)
	}
	if conv := string(data); !strings.Contains(conv, "no model") || !strings.Contains(conv, `"is_error":true`) {
		t.Fatalf("conversation missing persisted error turn:\n%s", conv)
	}

	// The next message retries the spawn instead of hitting a dead entry.
	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "again"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)
}
