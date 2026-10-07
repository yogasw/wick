package pool

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// silentSpawner hands out processes that take the prompt and never answer
// nor exit on their own — a server-mode turn whose stream went quiet.
type silentSpawner struct {
	mu    sync.Mutex
	calls int
}

func (s *silentSpawner) Spawn(context.Context, provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	s.calls++
	idx := s.calls
	s.mu.Unlock()
	pr, pw := io.Pipe()
	return &scriptedProc{
		stdoutR:  pr,
		stdoutW:  pw,
		stdinBuf: &bytes.Buffer{},
		done:     make(chan struct{}),
		pid:      71000 + idx,
	}, nil
}

func (s *silentSpawner) callsSnapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// The idle timer killing a respawn-per-turn agent (opencode here; omp and
// codex take the same path) is the agent's death, not a turn boundary: the
// session goes idle, the slot is freed and the queue moves. Before, the kill
// raced the reader into a clean exit, the pool kept the slot for a "next
// turn" that never came, and the session read "running" for hours.
func TestIdleKillOfRespawnAgentReleasesSlotAndRunsQueue(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	sp := &silentSpawner{}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}
	p := New(PoolConfig{
		MaxConcurrent: 1,
		IdleTimeout:   150 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
	})
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	for _, id := range []string{"S1", "S2"} {
		if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: id, Origin: session.OriginUI}); err != nil {
			t.Fatal(err)
		}
		if err := session.AddAgent(layout, id, "main", "opencode"); err != nil {
			t.Fatal(err)
		}
	}

	if err := p.Send(context.Background(), "S1", "main", "ui", "user", "long job"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return sp.callsSnapshot() == 1 }, 2*time.Second)
	// One slot, taken by S1: S2 has to wait for it.
	if err := p.Send(context.Background(), "S2", "main", "ui", "user", "next"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return sp.callsSnapshot() == 2 }, 3*time.Second)
	waitFor(t, func() bool {
		sess, err := session.Load(layout, "S1")
		return err == nil && sess.Meta.Status == session.StatusIdle
	}, 2*time.Second)
	for _, k := range p.ActiveSessions() {
		if k == "S1" || k == sessionKey("S1", "main") {
			t.Fatalf("S1 still holds a slot after its idle kill: %v", p.ActiveSessions())
		}
	}
}
