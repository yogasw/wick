package provider

import (
	"context"
	"testing"
	"time"
)

// An agent started with a ctx that is cancelled under it (a connector
// call's, returning once the message is "sent") stops reading its turn.
// An in-wick turn (omp RPC, opencode serve: Pid 0, no CommandContext)
// must be killed then — left alive it blocks writing to a stream nobody
// reads and holds its server's lease forever.
func TestCancelledRootCtxKillsInWickTurn(t *testing.T) {
	sp := newGatedSpawner()
	sp.keepOpen = true
	defer close(sp.release)
	a := newQueueAgent(t, sp, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	if err := a.Send("hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
	sp.mu.Lock()
	proc := sp.procs[0]
	sp.mu.Unlock()

	cancel()
	select {
	case <-proc.done:
	case <-time.After(3 * time.Second):
		t.Fatal("turn left running after its reader's ctx was cancelled")
	}
}
