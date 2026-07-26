package wick

import (
	"context"
	"sync"
	"time"
)

// livephase.go tracks, in memory, the live state of an in-process wick engine's
// current outbound model (LLM) call for a given session: whether HTTP is out to
// the vendor, since when, which retry attempt it's on, a snapshot of the request
// being sent (so the FE can show/copy it BEFORE any record lands — records are
// written only when a call finishes), and a cancel func to abort just this call
// without killing the whole turn/session.
//
// The interactions log alone can't tell "waiting on the model after a tool" from
// "the tool is still running", and it never carries the in-flight request. This
// registry closes both gaps without polluting the append-only log: the engine
// updates the phase around each call; the interactions HTTP handler reads it so
// the FE can label the live row, render the in-flight request, show a retry
// badge, and drive a per-call cancel.

// modelCallRequest is the snapshot of what's being sent to the vendor for the
// in-flight call. It mirrors interactionRecord's shape closely enough that the
// same curl builder + viewer render it, but it's captured BEFORE the call
// returns (the record only exists afterward).
type modelCallRequest struct {
	Kind    string           // "generate" | "compaction"
	Model   string           // model name
	ModelID string           // WickModel.ID → base_url/api_format/key lookup for curl
	System  string           // truncated system prompt
	Tools   []string         // offered tool names
	Request []interactionMsg // truncated message history
}

// modelCallPhase is one session's current model-call state.
type modelCallPhase struct {
	inFlight bool
	since    time.Time
	// attempt is the 1-based retry attempt of the CURRENT logical call. 1 = first
	// try; >1 means a previous attempt failed and this is a retry (overflow
	// compaction, or an HTTP 429/5xx/transport retry surfaced up from the
	// adapter). retryReason is a short human label ("rate limited", "server
	// error", "context full", …) shown next to the attempt.
	attempt     int
	retryReason string
	req         *modelCallRequest  // snapshot of the in-flight request (nil until set)
	cancel      context.CancelFunc // aborts just this model call (not the turn)
}

var (
	livePhaseMu sync.RWMutex
	livePhases  = map[string]modelCallPhase{} // sessionID → phase
)

// markModelCallStart records that session sid has an outbound model call in
// flight as of now, on the given attempt, with the request snapshot and a cancel
// func for this call. Cleared by markModelCallDone. attempt<1 is treated as 1.
func markModelCallStart(sid string, attempt int, req *modelCallRequest, cancel context.CancelFunc) {
	if sid == "" {
		return
	}
	if attempt < 1 {
		attempt = 1
	}
	livePhaseMu.Lock()
	livePhases[sid] = modelCallPhase{
		inFlight: true,
		since:    time.Now(),
		attempt:  attempt,
		req:      req,
		cancel:   cancel,
	}
	livePhaseMu.Unlock()
}

// markModelCallRetry updates the live phase to reflect that the current call is
// being retried: bumps the attempt and records why. The elapsed clock is reset
// so the timer reflects the new attempt, and a fresh cancel func is stored.
func markModelCallRetry(sid string, attempt int, reason string, req *modelCallRequest, cancel context.CancelFunc) {
	if sid == "" {
		return
	}
	if attempt < 1 {
		attempt = 1
	}
	livePhaseMu.Lock()
	livePhases[sid] = modelCallPhase{
		inFlight:    true,
		since:       time.Now(),
		attempt:     attempt,
		retryReason: reason,
		req:         req,
		cancel:      cancel,
	}
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

// ModelCallState is the live model-call snapshot the interactions HTTP handler
// serves to the FE. Zero value (InFlight=false) means idle.
type ModelCallState struct {
	InFlight    bool
	Elapsed     time.Duration
	Attempt     int
	RetryReason string
	// Request is the in-flight request snapshot, or nil. Exposed as the same
	// shape the finished-record curl/viewer use.
	Kind    string
	Model   string
	ModelID string
	System  string
	Tools   []string
	Messages []interactionMsg
}

// ModelCallInFlight reports whether an outbound model call is currently in
// flight for the session, and if so how long it has been running. Kept for
// existing callers; prefer ModelCallSnapshot for the full state.
func ModelCallInFlight(sid string) (bool, time.Duration) {
	livePhaseMu.RLock()
	p, ok := livePhases[sid]
	livePhaseMu.RUnlock()
	if !ok || !p.inFlight {
		return false, 0
	}
	return true, time.Since(p.since)
}

// ModelCallSnapshot returns the full live state for a session, including the
// in-flight request (for curl/viewer) and retry attempt. Exported for the
// interactions HTTP handler.
func ModelCallSnapshot(sid string) ModelCallState {
	livePhaseMu.RLock()
	p, ok := livePhases[sid]
	livePhaseMu.RUnlock()
	if !ok || !p.inFlight {
		return ModelCallState{}
	}
	st := ModelCallState{
		InFlight:    true,
		Elapsed:     time.Since(p.since),
		Attempt:     p.attempt,
		RetryReason: p.retryReason,
	}
	if p.req != nil {
		st.Kind = p.req.Kind
		st.Model = p.req.Model
		st.ModelID = p.req.ModelID
		st.System = p.req.System
		st.Tools = p.req.Tools
		st.Messages = p.req.Request
	}
	return st
}

// CancelModelCall aborts just the in-flight model call for a session (not the
// turn or the whole spawn). Returns true if there was a call to cancel. The
// engine's generate() wraps the vendor call in a child context whose cancel func
// is stored here; cancelling it makes GenerateContent return, generate() records
// the aborted attempt, and the turn continues (the engine sees the error and
// ends the turn gracefully rather than the process dying).
func CancelModelCall(sid string) bool {
	livePhaseMu.Lock()
	p, ok := livePhases[sid]
	livePhaseMu.Unlock()
	if !ok || !p.inFlight || p.cancel == nil {
		return false
	}
	p.cancel()
	return true
}
