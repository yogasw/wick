package registry

import (
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// deferredRecoveryInterval is how often sessions Reload passed over are
// looked at again. A var so tests can drive recoverDeferred directly
// without a watcher racing them.
var deferredRecoveryInterval = 2 * time.Second

// resetSessionStatus forces a session whose subprocess is gone back to
// idle, on disk and in s. cli_session_id is preserved for resume.
func resetSessionStatus(layout config.Layout, s *session.Session) {
	dirty := false
	if s.Meta.Status != session.StatusIdle {
		s.Meta.Status = session.StatusIdle
		dirty = true
	}
	for i := range s.Agents {
		if s.Agents[i].Status != "idle" {
			s.Agents[i].Status = "idle"
			dirty = true
		}
	}
	if dirty {
		_ = session.SaveMeta(layout, s.ID, s.Meta)
		_ = session.SaveAgents(layout, s.ID, s.Agents)
	}
}

// recoverSessionInflight folds a leftover inflight.jsonl into the
// session's conversation as a truncated turn of its active agent. The
// caller holds the session's turn lock.
func recoverSessionInflight(layout config.Layout, s session.Session) {
	recoveryAgent := s.Meta.ActiveAgent
	if recoveryAgent == "" && len(s.Agents) > 0 {
		recoveryAgent = s.Agents[0].Name
	}
	recoveryProvider := ""
	for _, a := range s.Agents {
		if a.Name == recoveryAgent {
			recoveryProvider = a.Provider
			break
		}
	}
	if recovered, err := store.RecoverInflight(layout, s.ID, recoveryAgent, recoveryProvider, nil); err != nil {
		log.Warn().Err(err).Str("session", s.ID).Msg("registry: recover inflight failed")
	} else if recovered {
		log.Info().Str("session", s.ID).Str("agent", recoveryAgent).Msg("registry: recovered inflight turn into conversation.jsonl")
	}
}

// deferRecovery queues sessions whose turn was alive in another process at
// boot and starts one watcher that re-checks them until their turn lock
// frees. Polling, not the upgrade's parent-exit signal: the lock is per
// session and frees the moment that turn ends, usually long before the
// predecessor finishes draining everything else, and it covers a turn of
// any other process, not only a tableflip parent.
func (r *Registry) deferRecovery(ids map[string]time.Time) {
	if len(ids) == 0 {
		return
	}
	r.deferMu.Lock()
	if r.deferred == nil {
		r.deferred = map[string]time.Time{}
	}
	start := len(r.deferred) == 0
	for id, at := range ids {
		r.deferred[id] = at
	}
	r.deferMu.Unlock()
	if start {
		go r.watchDeferredRecovery()
	}
}

// watchDeferredRecovery re-checks the deferred sessions until none is left.
func (r *Registry) watchDeferredRecovery() {
	for {
		time.Sleep(deferredRecoveryInterval)
		if r.recoverDeferred() == 0 {
			return
		}
	}
}

// recoverDeferred settles every deferred session whose turn lock is free
// now and returns how many are still waiting.
func (r *Registry) recoverDeferred() int {
	r.deferMu.Lock()
	pending := make(map[string]time.Time, len(r.deferred))
	for id, at := range r.deferred {
		pending[id] = at
	}
	r.deferMu.Unlock()
	for id, at := range pending {
		if !r.recoverDeferredSession(id, at) {
			continue
		}
		r.deferMu.Lock()
		delete(r.deferred, id)
		r.deferMu.Unlock()
	}
	r.deferMu.Lock()
	defer r.deferMu.Unlock()
	return len(r.deferred)
}

// recoverDeferredSession settles one passed-over session once its turn
// lock is free; false while the turn still runs.
//
// The other process ended its turn one of two ways. Finished: it removed
// inflight.jsonl and wrote its own status, so only the cache needs the
// disk copy. Died mid-turn: inflight.jsonl is still there and the status
// it left is a lie — that is the boot recovery Reload skipped, done now.
// The status is reset only while LastActive is still the one seen at boot:
// a newer one means this process already started a turn there, and that
// status is real.
func (r *Registry) recoverDeferredSession(id string, bootLastActive time.Time) bool {
	guard, free := store.TryLockInflight(r.layout, id)
	if !free {
		return false
	}
	defer guard.Release()
	s, err := session.Load(r.layout, id)
	if err != nil {
		// Deleted while we waited: nothing left to recover.
		return true
	}
	if _, err := os.Stat(r.layout.SessionInflight(id)); err == nil {
		if s.Meta.LastActive.Equal(bootLastActive) {
			resetSessionStatus(r.layout, &s)
		}
		recoverSessionInflight(r.layout, s)
	}
	r.upsertSession(s)
	return true
}
