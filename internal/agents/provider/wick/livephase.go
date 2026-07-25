package wick

import (
	"sync"
	"time"
)

// livephase.go tracks, in memory, whether an in-process wick engine is
// currently BLOCKED on an outbound model (LLM) call for a given session — i.e.
// HTTP is out to the vendor and no record has landed yet (records are written
// only when a call finishes). The interactions log alone can't tell "waiting on
// the model after a tool" from "the tool is still running", because both look
// like "the newest record has tool_calls". This registry closes that gap
// without polluting the append-only log: the engine marks the phase when a call
// starts and clears it when the call returns; the interactions HTTP handler
// reads it so the FE can label the live row accurately.

// modelCallPhase is one session's current model-call state.
type modelCallPhase struct {
	inFlight bool
	since    time.Time
}

var (
	livePhaseMu sync.RWMutex
	livePhases  = map[string]modelCallPhase{} // sessionID → phase
)

// markModelCallStart records that session sid has an outbound model call in
// flight as of now. Cleared by markModelCallDone.
func markModelCallStart(sid string) {
	if sid == "" {
		return
	}
	livePhaseMu.Lock()
	livePhases[sid] = modelCallPhase{inFlight: true, since: time.Now()}
	livePhaseMu.Unlock()
}

// markModelCallDone clears the in-flight mark for session sid (the call
// returned — a record is about to be written, so the phase is no longer
// "waiting on the model").
func markModelCallDone(sid string) {
	if sid == "" {
		return
	}
	livePhaseMu.Lock()
	delete(livePhases, sid)
	livePhaseMu.Unlock()
}

// ModelCallInFlight reports whether an outbound model call is currently in
// flight for the session, and if so how long it has been running. Exported for
// the interactions HTTP handler. Returns (false, 0) when idle/unknown.
func ModelCallInFlight(sid string) (bool, time.Duration) {
	livePhaseMu.RLock()
	p, ok := livePhases[sid]
	livePhaseMu.RUnlock()
	if !ok || !p.inFlight {
		return false, 0
	}
	return true, time.Since(p.since)
}
