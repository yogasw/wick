package pool

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSendDuringSpawnWindowIsDelivered: a message sent after a spawn has
// published its entry and drained the buffer, but before the process
// started, is neither left in the buffer (its sender leaves it to the
// drain already done) nor allowed to evict the entry being started.
// endSpawn hands it over.
func TestSendDuringSpawnWindowIsDelivered(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")
	var once sync.Once
	entered, release := make(chan struct{}), make(chan struct{})
	p.foStage = func(stage string) {
		if stage == "spawn-drained" {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
	}

	first := make(chan error, 1)
	go func() { first <- p.Send(context.Background(), "S1", "default", "ui", "user", "m1") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("spawn never drained")
	}
	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "m2"); err != nil {
		t.Fatalf("send m2: %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("send m1: %v", err)
	}
	waitFor(t, func() bool {
		sp.mu.Lock()
		last := sp.Last
		sp.mu.Unlock()
		if last == nil {
			return false
		}
		in := last.recordedStdin()
		return strings.Contains(in, "m1") && strings.Contains(in, "m2")
	}, 5*time.Second)
	sp.mu.Lock()
	calls := sp.Calls
	sp.mu.Unlock()
	if calls != 1 {
		t.Fatalf("spawned %d processes, want 1", calls)
	}
}
