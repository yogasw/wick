package delegation

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// InterruptOutcome distinguishes the three ways a stop request can land.
// They are separate because the UI must react differently to each: two
// are success, one is a benign race that must NOT overwrite a result the
// user is already looking at.
type InterruptOutcome string

const (
	// OutcomeKilled — the child was running; its process was stopped and
	// partial work was captured.
	OutcomeKilled InterruptOutcome = "killed"
	// OutcomeDequeued — the child had not started yet; it was removed
	// from the queue. No process existed to kill.
	OutcomeDequeued InterruptOutcome = "dequeued"
	// OutcomeAlreadyDone — the child reached a terminal state first.
	// Maps to HTTP 409. The successful result stands.
	OutcomeAlreadyDone InterruptOutcome = "already_done"
)

// Interrupt stops one delegation at a human's request.
//
// Idempotent by construction: the terminal write is guarded on the
// expected prior status, so a completion that lands concurrently wins
// and this reports OutcomeAlreadyDone instead of overwriting a good
// result with "interrupted". Calling it twice is safe — the second call
// is a no-op that reports the race.
//
// Ordering is deliberate: partial output is captured BEFORE the process
// is killed, and a failure to persist never blocks the kill. Stopping
// the work is the thing the user asked for; losing a few lines of
// transcript is the lesser failure.
func (s *Service) Interrupt(ctx context.Context, delegationID, actorID string, isAdmin bool) (InterruptOutcome, error) {
	d, err := s.Repo.Get(ctx, delegationID)
	if err != nil {
		return "", err
	}
	if !CanInterrupt(d, actorID, isAdmin) {
		return "", ErrForbidden
	}

	// Cheap pre-check: skip the kill entirely when the row is already
	// closed. This is an optimisation, not the correctness guarantee —
	// the guarded write below is what actually makes the race safe.
	if entity.IsTerminalDelegationStatus(d.Status) {
		return OutcomeAlreadyDone, nil
	}

	// A queued child has no process yet. Killing is not just useless
	// here, it is the bug that makes a Stop button look like it did
	// nothing: without this branch the call finds no live agent, treats
	// it as an error, and the row stays queued and later runs anyway.
	if d.Status == entity.DelegationQueued {
		s.cancelInflight(delegationID)
		ok, err := s.Repo.FinishGuarded(ctx, delegationID,
			entity.DelegationQueued, entity.DelegationInterrupted,
			"(cancelled before processing)", "", d.TurnsUsed)
		if err != nil {
			return "", err
		}
		if !ok {
			return OutcomeAlreadyDone, nil
		}
		// Cancelling a queued item does not free a slot (it never held
		// one), but it does change who is at the head of the line — the
		// next item must be started rather than waiting for a sweep.
		s.pokeSlot(d.RootID)
		return OutcomeDequeued, nil
	}

	// Running: capture partial output first.
	partial := ""
	if s.Runner != nil {
		partial = s.Runner.PartialText(d.ChildSessionID, d.ChildAgent)
	}

	// Unblock any Run waiting on this child. When a waiter exists it owns
	// the terminal write and the kill, so returning here avoids two paths
	// racing to close the same row.
	if s.cancelInflight(delegationID) {
		return OutcomeKilled, nil
	}

	// No local waiter (e.g. the leader's request already went away):
	// stop the process directly. A missing agent is not a failure — it
	// means the child exited on its own between the status read and now,
	// and the status still needs writing.
	if s.Runner != nil {
		if err := s.Runner.KillAgent(d.ChildSessionID, d.ChildAgent); err != nil {
			log.Debug().Err(err).Str("delegation", delegationID).
				Msg("delegation.interrupt: kill reported no live agent; recording status anyway")
		}
	}

	ok, err := s.Repo.FinishGuarded(ctx, delegationID,
		entity.DelegationRunning, entity.DelegationInterrupted,
		partial, "", d.TurnsUsed)
	if err != nil {
		return "", err
	}
	if !ok {
		return OutcomeAlreadyDone, nil
	}
	// A stopped sub-agent frees its slot; the queue must move immediately
	// rather than idling until the next sweep.
	s.pokeSlot(d.RootID)
	return OutcomeKilled, nil
}

// InterruptAll stops every live sub-agent under sessionID, deepest
// first, and reports how many were stopped.
//
// Deepest-first matters: stopping a parent before its own children can
// leave grandchildren running with nothing waiting on them.
//
// This stops the CHILDREN only — the leader keeps running and can react
// to the partial results, which is what "Stop all" means from the
// panel. Killing the leader too is the separate cascade path.
//
// cascade distinguishes the two callers. An explicit "Stop all" is a
// direct instruction and stops everything, including detached async
// sub-agents. A cascade from killing the leader is INDIRECT, and there it
// would be wrong to take down work that was fired precisely so it would
// keep running on its own.
func (s *Service) InterruptAll(ctx context.Context, sessionID, actorID string, isAdmin bool) (int, error) {
	return s.interruptAll(ctx, sessionID, actorID, isAdmin, false)
}

// CascadeInterrupt stops a session's descendants as a side effect of the
// leader being killed, leaving detached async work alone.
func (s *Service) CascadeInterrupt(ctx context.Context, sessionID, actorID string, isAdmin bool) (int, error) {
	return s.interruptAll(ctx, sessionID, actorID, isAdmin, true)
}

func (s *Service) interruptAll(ctx context.Context, sessionID, actorID string, isAdmin bool, cascade bool) (int, error) {
	rows, err := s.Repo.ListActiveDescendants(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		d := rows[i]
		if !CanInterrupt(&d, actorID, isAdmin) {
			continue
		}
		// A detached (async) sub-agent was fired deliberately to outlive
		// its leader, so leader teardown must not sweep it up. Stopping one
		// stays possible — just explicitly, by its own Stop button, not as
		// collateral from killing the conversation that started it.
		if d.Detached && cascade {
			continue
		}
		out, err := s.Interrupt(ctx, d.ID, actorID, isAdmin)
		if err != nil {
			// One stubborn child must not abort the sweep — the point of
			// Stop all is that nothing is left running.
			log.Warn().Err(err).Str("delegation", d.ID).Msg("delegation: interrupt-all entry failed")
			continue
		}
		if out == OutcomeKilled || out == OutcomeDequeued {
			n++
		}
	}
	return n, nil
}
