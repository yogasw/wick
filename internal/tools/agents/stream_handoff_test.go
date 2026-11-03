package agents

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A stream moves to the successor once this process drains, but not while
// a turn of its session still runs here; one for no session moves at once.
func TestStreamHandoff(t *testing.T) {
	var draining, busy atomic.Bool
	prevEvery, prevDraining, prevTurn := streamHandoffEvery, streamDraining, streamTurnHere
	streamHandoffEvery = 5 * time.Millisecond
	streamDraining = draining.Load
	streamTurnHere = func(id string) bool { return id == "s-busy" && busy.Load() }
	t.Cleanup(func() { streamHandoffEvery, streamDraining, streamTurnHere = prevEvery, prevDraining, prevTurn })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := func(ch <-chan struct{}) bool {
		select {
		case <-ch:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}

	idle := streamHandoff(ctx, "s-idle")
	held := streamHandoff(ctx, "s-idle", "s-busy")
	global := streamHandoff(ctx)
	busy.Store(true)
	if closed(idle) || closed(global) {
		t.Fatal("a stream moved before draining")
	}

	draining.Store(true)
	if !closed(idle) || !closed(global) {
		t.Fatal("a stream with nothing left here did not move once draining")
	}
	if closed(held) {
		t.Fatal("a stream moved while its session's turn still runs here")
	}
	busy.Store(false)
	if !closed(held) {
		t.Fatal("a stream did not move after its turn ended")
	}

	// A client that leaves first ends the watcher without a close.
	draining.Store(false)
	gone, stop := context.WithCancel(context.Background())
	left := streamHandoff(gone, "s-idle")
	stop()
	draining.Store(true)
	if closed(left) {
		t.Fatal("a stream whose client left was closed")
	}
}
