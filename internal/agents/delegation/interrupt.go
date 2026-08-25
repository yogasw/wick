package delegation

import (
	"context"
	"strings"

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

	// The row being closed does NOT mean the process is gone. A
	// prematurely closed delegation (a turn-boundary text mistaken for
	// the answer, a lost event) leaves the sub-agent alive and working
	// with nobody supervising it — and answering "already_done" here
	// while its progress kept flowing was exactly the observed bug: Stop
	// became a no-op on the one process the operator most wanted dead.
	// Kill any survivor first; KillAgent on a dead agent is a cheap
	// ErrAgentNotActive.
	if entity.IsTerminalDelegationStatus(d.Status) {
		if s.Runner != nil && d.ChildSessionID != "" {
			if err := s.Runner.KillAgent(d.ChildSessionID, d.ChildAgent); err == nil {
				log.Warn().Str("delegation", delegationID).
					Str("child_session", d.ChildSessionID).
					Msg("delegation.interrupt: row was already terminal but the agent was still alive; killed it")
			}
		}
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

	// Running: capture whatever work exists before killing it.
	//
	// PartialText is the in-flight turn's buffer, which is empty between
	// turns — and a respawn-per-turn provider (codex) is between turns for
	// most of its life. Stopping one there recorded result:"" and the
	// operator lost every completed step, even though the sub-agent had
	// been reporting them all along.
	//
	// So fall back to its last progress note. It is not the whole answer
	// and does not pretend to be, but "steps 1-4 done, starting 5" is the
	// difference between a partial result and none.
	partial := ""
	if s.Runner != nil {
		partial = strings.TrimSpace(s.Runner.PartialText(d.ChildSessionID, d.ChildAgent))
	}
	if partial == "" && strings.TrimSpace(d.LastReport) != "" {
		partial = "(stopped mid-run; this is the sub-agent's last progress report, not a finished answer)\n\n" +
			strings.TrimSpace(d.LastReport)
	}

	// Kill FIRST, unconditionally, before handing off to any waiter.
	//
	// This used to return as soon as cancelInflight found a waiter, on the
	// grounds that the waiter owns the kill. It does — but only once its
	// select actually reaches the ctx.Done branch, and a child that is
	// streaming keeps that select busy on event branches. Meanwhile a
	// respawn-per-turn provider (codex) starts its NEXT turn from a
	// process the waiter has not been told about yet.
	//
	// The observed result: Stop answered "killed", the row went
	// interrupted, and the sub-agent worked on to step 10 — only its
	// progress calls started failing, because the row was terminal. Work
	// carried on with nobody watching, which is the opposite of what
	// pressing Stop asks for.
	//
	// Killing here is safe to do twice: KillAgent on an already-stopped
	// agent returns ErrAgentNotActive, and the waiter's own kill is a
	// no-op on a dead process.
	if s.Runner != nil {
		if err := s.Runner.KillAgent(d.ChildSessionID, d.ChildAgent); err != nil {
			log.Debug().Err(err).Str("delegation", delegationID).
				Msg("delegation.interrupt: kill reported no live agent; recording status anyway")
		}
	}

	// Then unblock any Run waiting on this child. When a waiter exists it
	// owns the terminal write, so returning here avoids two paths racing
	// to close the same row.
	if s.cancelInflight(delegationID) {
		return OutcomeKilled, nil
	}

	// Re-read the turn count instead of reusing the snapshot taken at the
	// top of this function. The child kept working while we killed it, and
	// UpdateTurns writes on every Done — so the snapshot can be several
	// turns stale, and writing it back walks the counter BACKWARDS. An
	// interrupted delegation then reports fewer turns than it really spent
	// (observed: 1 → 0), which under-reports the bill and makes a
	// continuation's budget arithmetic wrong.
	turns := d.TurnsUsed
	if fresh, ferr := s.Repo.Get(ctx, delegationID); ferr == nil && fresh.TurnsUsed > turns {
		turns = fresh.TurnsUsed
	}

	ok, err := s.Repo.FinishGuarded(ctx, delegationID,
		entity.DelegationRunning, entity.DelegationInterrupted,
		partial, "", turns)
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
	// An explicit "Stop all" leaves nothing running, so it has no survivors to
	// report — the cascade path is the only caller that needs them.
	n, _, err := s.interruptAll(ctx, sessionID, actorID, isAdmin, false)
	return n, err
}

// CascadeInterrupt stops a session's descendants as a side effect of the
// leader being killed, leaving detached async work alone.
func (s *Service) CascadeInterrupt(ctx context.Context, sessionID, actorID string, isAdmin bool) (int, error) {
	n, _, err := s.CascadeInterruptReport(ctx, sessionID, actorID, isAdmin)
	return n, err
}

// Survivor names one sub-agent that a cascade deliberately left running.
//
// Killing a leader does not stop its detached async children, which is the
// intended behaviour but is invisible from the conversation: the thread shows
// the leader stopping and then nothing, while work continues. Reporting the
// survivors lets a channel say so.
type Survivor struct {
	Handle     string // addressable handle within the tree, e.g. "researcher-1"
	ProfileKey string // role that was delegated to
	AgentName  string // pool agent name, for correlating with relayed progress
}

// CascadeInterruptReport is CascadeInterrupt plus the list of detached
// sub-agents it left running, in the order encountered.
func (s *Service) CascadeInterruptReport(ctx context.Context, sessionID, actorID string, isAdmin bool) (int, []Survivor, error) {
	return s.interruptAll(ctx, sessionID, actorID, isAdmin, true)
}

func (s *Service) interruptAll(ctx context.Context, sessionID, actorID string, isAdmin bool, cascade bool) (int, []Survivor, error) {
	rows, err := s.Repo.ListActiveDescendants(ctx, sessionID)
	if err != nil {
		return 0, nil, err
	}
	n := 0
	var survivors []Survivor
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
			survivors = append(survivors, Survivor{
				Handle: d.Handle, ProfileKey: d.ProfileKey, AgentName: d.ChildAgent,
			})
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
	return n, survivors, nil
}
