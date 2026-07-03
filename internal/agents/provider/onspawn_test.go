package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// TestOnSpawnFiresOnRespawn verifies the spawn-log fix: respawn-per-turn
// providers (codex) spawn the real process in respawnWithMessage, not at
// Start(), so the per-turn OnSpawn hook is what carries pid + the
// triggering message to the spawn log. Without it the log shows a blank
// start with no Reproduce command.
func TestOnSpawnFiresOnRespawn(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		codexLines("sess-1", "hi back"),
	}}
	st := state.New(nil)

	var mu sync.Mutex
	var gotPID int
	var gotMsg string
	calls := 0

	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         st,
		SendMode:      SendRespawnQueue,
		OnSpawn: func(binary string, argv []string, env []string, pid int, firstMessage string) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			gotPID = pid
			gotMsg = firstMessage
		},
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Start() must NOT spawn (deferred) → OnSpawn not yet fired.
	mu.Lock()
	if calls != 0 {
		mu.Unlock()
		t.Fatalf("OnSpawn fired before first Send (calls=%d)", calls)
	}
	mu.Unlock()

	if err := a.Send("hello codex"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 1
	}, time.Second)

	mu.Lock()
	defer mu.Unlock()
	if gotMsg != "hello codex" {
		t.Errorf("OnSpawn firstMessage = %q, want %q", gotMsg, "hello codex")
	}
	if gotPID == 0 {
		t.Errorf("OnSpawn pid = 0, want the spawned process pid")
	}
}
