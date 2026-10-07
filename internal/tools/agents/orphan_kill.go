package agents

import (
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/processctl"
)

// orphanStopDetail explains, in the spawn log, why a process was ended by
// something other than the pool that started it.
const orphanStopDetail = "stopped by the user — the process was orphaned by a wick restart, " +
	"so the pool that answered the request had no handle on it"

// killOrphanSpawns ends the processes of a session that THIS wick does not
// own.
//
// Stop goes through the pool, and the pool only knows the agents it started.
// After a binary swap the previous wick keeps running the turns it had in
// flight — by design, so nobody's work is cut in half — but the new one
// serves the UI. Press Stop on such a session and pool.KillBy walks an empty
// map and reports success: the button says killed, the agent keeps going,
// and the row keeps saying running. That is the bug this closes.
//
// The spawn log is the only record that survives the swap, and it carries
// the pid. So when the pool has nothing, fall back to it: end the pids it
// says are still running for this session, and write the exit event that
// the dead wick will never write.
//
// Sub-agents are included. Their session ids are the parent's plus a
// "--sub-<id>" suffix, and a Stop that leaves them running leaves work
// nobody is waiting for spending tokens.
func killOrphanSpawns(sessionID string) []int {
	if globalSpawnLog == nil || sessionID == "" {
		return nil
	}
	return killOrphanSpawnsWith(globalSpawnLog, sessionID, processctl.ProcessAlive, terminate)
}

// killOrphanSpawnsWith is killOrphanSpawns with its two side effects
// injected, so the selection rules can be tested without ending real
// processes.
func killOrphanSpawnsWith(
	logger *provider.SpawnLogger,
	sessionID string,
	alive func(pid int) bool,
	kill func(pid int) error,
) []int {
	files, err := logger.List("", "", "")
	if err != nil {
		return nil
	}
	var killed []int
	seen := map[string]bool{}
	for _, f := range files {
		if !sessionMatches(f.SessionID, sessionID) {
			continue
		}
		// List is newest-first, so the first log seen for a session id is
		// its current spawn. An older one is history: its pid may well have
		// been recycled by an unrelated process, and killing that would be
		// the worst kind of bug to ship in a Stop button.
		if seen[f.SessionID] {
			continue
		}
		seen[f.SessionID] = true
		if f.ExitReason != "" || f.PID <= 0 || !alive(f.PID) {
			continue
		}
		if err := kill(f.PID); err != nil {
			continue
		}
		killed = append(killed, f.PID)
		_ = logger.Append(f.Path, provider.SpawnEvent{
			Type:         "exit",
			At:           time.Now().UTC(),
			ProviderType: f.ProviderType,
			ProviderName: f.ProviderName,
			SessionID:    f.SessionID,
			ExitReason:   "stopped",
			ReasonDetail: orphanStopDetail,
			PID:          f.PID,
		})
	}
	return killed
}

// sessionPIDs is the pids this pool believes are running for a session,
// including its sub-agents.
func sessionPIDs(sessionID string) []int {
	if globalPool == nil {
		return nil
	}
	var pids []int
	for _, e := range globalPool.ActiveSnapshot() {
		if e.PID > 0 && sessionMatches(e.SessionID, sessionID) {
			pids = append(pids, e.PID)
		}
	}
	return pids
}

// enforceStop makes sure the processes a Stop was supposed to end are
// actually gone, and ends the ones that are not.
//
// Agent.Stop terminates through the exec handle it kept when it STARTED the
// process. An entry this wick adopted rather than spawned — what a binary
// swap leaves behind — has no such handle, so Stop tears down the
// bookkeeping, returns nil, and the process keeps running with the panel
// still showing it. Checking afterwards is the only way to tell that apart
// from a stop that worked: a pid that is still alive a moment later did not
// get the message.
//
// Returns the pids it had to signal itself.
func enforceStop(pids []int) []int {
	return enforceStopWith(pids, processctl.ProcessAlive, terminate,
		func() { time.Sleep(250 * time.Millisecond) }, 8)
}

// enforceStopWith is enforceStop with its waiting and killing injected, so
// the give-it-a-moment logic is testable without real processes or a real
// two seconds.
func enforceStopWith(pids []int, alive func(int) bool, kill func(int) error, wait func(), attempts int) []int {
	if len(pids) == 0 {
		return nil
	}
	remaining := append([]int(nil), pids...)
	for i := 0; i < attempts; i++ {
		still := remaining[:0:0]
		for _, pid := range remaining {
			if alive(pid) {
				still = append(still, pid)
			}
		}
		remaining = still
		if len(remaining) == 0 {
			// A clean stop is the normal case and must cost nothing extra:
			// return as soon as everything is gone rather than sitting out
			// the rest of the grace window.
			return nil
		}
		wait()
	}
	var signalled []int
	for _, pid := range remaining {
		if !alive(pid) {
			continue
		}
		if err := kill(pid); err != nil {
			continue
		}
		signalled = append(signalled, pid)
	}
	return signalled
}

// sessionMatches reports whether a spawn's session id is the session being
// stopped, or one of its sub-agents.
func sessionMatches(spawnSession, sessionID string) bool {
	return spawnSession == sessionID || strings.HasPrefix(spawnSession, sessionID+"--sub-")
}

// poolHasSession reports whether this wick's pool is actually running
// anything for a session — the question that decides whether Stop needs the
// fallback above.
func poolHasSession(sessionID string) bool {
	if globalPool == nil {
		return false
	}
	for _, e := range globalPool.ActiveSnapshot() {
		if sessionMatches(e.SessionID, sessionID) {
			return true
		}
	}
	return false
}
