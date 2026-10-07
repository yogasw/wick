package provider

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// busyProcess is a silent process that can report whether its turn is
// still running on a server.
type busyProcess struct {
	*fakeProcess
	busy *atomic.Bool
}

func (p busyProcess) Busy() bool { return p.busy.Load() }

type busySpawner struct {
	keepAliveSpawner
	busy *atomic.Bool
}

func (s *busySpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	p, err := s.keepAliveSpawner.Spawn(ctx, opt)
	if err != nil {
		return nil, err
	}
	return busyProcess{fakeProcess: p.(*fakeProcess), busy: s.busy}, nil
}

// A silent stream is not a dead turn when the process says the server is
// still working on it (a long tool, a sub-agent): the idle timer must not
// kill it, and must once the server stops being busy.
func TestAgentIdleTTLSparesBusyProcess(t *testing.T) {
	pr, pw := makePipePair()
	busy := &atomic.Bool{}
	busy.Store(true)
	spawner := &busySpawner{keepAliveSpawner: keepAliveSpawner{stdoutR: pr, stdoutW: pw}, busy: busy}
	exited := make(chan ExitReason, 1)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   100 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
		OnExit:        func(r ExitReason, _ string) { exited <- r },
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-exited:
		t.Fatalf("busy turn was killed (%v)", r)
	case <-time.After(600 * time.Millisecond): // six idle windows
	}
	busy.Store(false)
	select {
	case r := <-exited:
		if r != ExitIdle {
			t.Fatalf("exit reason: %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle TTL did not fire once the process went idle")
	}
}
