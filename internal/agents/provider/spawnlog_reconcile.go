package provider

import (
	"time"

	"github.com/yogasw/wick/internal/processctl"
)

// OrphanExitReason is what a spawn is marked with when its process is gone
// but nothing ever wrote its exit. It is deliberately not "crashed": we do
// not know how it died, only that nobody was left to record it.
const OrphanExitReason = "orphaned"

const orphanDetail = "no exit was recorded — wick was replaced or killed while this spawn was running, " +
	"so the process outlived the only thing watching it"

// ReconcileOrphans closes out spawn logs that claim to be running but whose
// process is gone.
//
// The exit event is written by the in-process hook that reaps the
// subprocess, which means a wick that is itself killed — a binary swap, a
// hard restart, an OOM of the daemon — never writes one. The spawn log then
// ends on `start`, and every reader of it (the Providers table, the session
// row, the Stop button) is told the spawn is still running, forever. Nothing
// reconciles that later: the next daemon has no memory of a process it did
// not start.
//
// So this runs at startup and before the Providers list is served: any log
// whose newest word is `start`, and whose recorded pid is no longer alive,
// gets the exit it never got. Returns how many it closed.
//
// A pid that IS alive is left alone. It may well be a process this daemon
// does not own — a spawn that survived a handover is exactly that — but it
// is running, and saying otherwise would be the same lie in the other
// direction.
func (s *SpawnLogger) ReconcileOrphans(alive func(pid int) bool) (int, error) {
	if alive == nil {
		alive = processctl.ProcessAlive
	}
	files, err := s.List("", "", "")
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, f := range files {
		if f.ExitReason != "" {
			continue
		}
		// pid 0 means the start event never carried one (a provider that
		// respawns per turn, or a spawn killed between the pre-start
		// record and the real one). There is no process to ask about, and
		// the file is not being appended to by anyone, so it is orphaned
		// by the same argument.
		if f.PID != 0 && alive(f.PID) {
			continue
		}
		ev := SpawnEvent{
			Type:         "exit",
			At:           time.Now().UTC(),
			ProviderType: f.ProviderType,
			ProviderName: f.ProviderName,
			SessionID:    f.SessionID,
			ExitReason:   OrphanExitReason,
			ReasonDetail: orphanDetail,
		}
		if !f.StartedAt.IsZero() {
			ev.DurationMs = ev.At.Sub(f.StartedAt).Milliseconds()
		}
		if err := s.Append(f.Path, ev); err != nil {
			continue
		}
		closed++
	}
	return closed, nil
}
