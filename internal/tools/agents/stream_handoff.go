package agents

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/pkg/upgrade"
)

// streamHandoffEvery is how often an open SSE stream checks whether it
// should move to the successor. A var so tests need not wait.
var streamHandoffEvery = 2 * time.Second

// streamDraining and streamTurnHere are the two facts the handoff reads.
// Vars so tests can stand in for the upgrade flag and the pool.
var (
	streamDraining = upgrade.Draining
	streamTurnHere = poolHasTurn
)

// streamHandoff closes once this process is draining after a handover and
// none of sessions has a turn running in its pool. A draining process
// keeps its open streams until all of its own work settles, and while it
// does a browser watching such a stream sees nothing of the turns the
// successor runs: no working state, no stop button, no trace. Closing it
// lets the client reconnect to the successor, which owns the socket and
// replays the session's live state on connect. A session whose turn this
// process is still finishing keeps its stream until that turn ends. A
// stream for no session (sidebar, global) moves as soon as draining starts.
func streamHandoff(ctx context.Context, sessions ...string) <-chan struct{} {
	// Read the knobs once, here: the watcher may outlive a test that swaps
	// them back, and reading them inside the goroutine would race that swap.
	every, draining, turnHere := streamHandoffEvery, streamDraining, streamTurnHere
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !draining() {
					continue
				}
				busy := false
				for _, id := range sessions {
					if turnHere(id) {
						busy = true
						break
					}
				}
				if !busy {
					close(done)
					return
				}
			}
		}
	}()
	return done
}

// poolHasTurn reports whether this process's pool runs (or is about to run)
// a turn for sessionID.
func poolHasTurn(sessionID string) bool {
	if globalPool == nil {
		return false
	}
	for _, a := range globalPool.ActiveSnapshot() {
		if a.SessionID == sessionID && (a.Lifecycle == "spawning" || a.Lifecycle == "working" || a.Lifecycle == "queued") {
			return true
		}
	}
	return false
}
