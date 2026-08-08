package delegation

import (
	"context"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Async collect (Phase 4).
//
// An async delegation returns a handle immediately, so the leader needs
// a way to pick the answer up later. Two routes exist and they are
// complementary:
//
//   - delivery_sink=session pushes the result back by re-prompting the
//     leader when it lands (the callback path, in Run).
//   - wick_delegate_collect pulls, for a leader that would rather ask
//     than be woken.
//
// Both mark the row Collected with a guarded write, so a result is never
// handed over twice — a leader that acts on the same sub-agent answer
// twice will duplicate whatever it did with it.

// CollectResult is one collected async delegation.
type CollectResult struct {
	DelegationID string `json:"delegation_id"`
	Profile      string `json:"profile"`
	Status       string `json:"status"`
	TurnsUsed    int    `json:"turns_used"`
	TokensUsed   int    `json:"tokens_used,omitempty"`
	Result       string `json:"result"`
	Note         string `json:"note,omitempty"`
	UserSteered  bool   `json:"user_steered,omitempty"`
	// Pending marks a delegation that has not finished yet: Result is
	// empty and the leader should carry on rather than wait here.
	Pending bool `json:"pending,omitempty"`
	// Progress is what a STILL-RUNNING sub-agent has produced so far,
	// scraped from its in-flight turn. Empty on a terminal row, where
	// Result is the real answer.
	//
	// It is a peek at work in flight, not a verdict: the sub-agent may
	// contradict it before the turn ends. Its purpose is to let a
	// supervising agent notice work going the wrong way EARLY, while a
	// correction is still cheap — waiting for a terminal status means
	// reviewing a wrong direction after it has been fully taken.
	Progress string `json:"progress,omitempty"`
	// LastReport is the sub-agent's own most recent progress note, if it
	// filed one. Preferred over Progress when judging direction: it is the
	// agent stating where it is, rather than a scrape of its prose.
	LastReport string `json:"last_report,omitempty"`
	// Envelope is the sub-agent's answer as typed fields, when it has one.
	Envelope *ResultEnvelope `json:"envelope,omitempty"`
}

// pendingNote is what a leader is told about a delegation still working.
//
// It separates two things the previous wording ran together. "Do not
// block on this" is right for a caller waiting on the ANSWER — that is
// what the sink is for. It is wrong for a supervising agent deliberately
// checking on progress, which is now a supported thing to do and the only
// way to catch a sub-agent going astray before it finishes.
const pendingNote = "Still working — this is a progress check, not the answer. The result arrives on its own; " +
	"do not sit in a loop waiting for it here. If the progress below shows the wrong direction, correct it with " +
	"message rather than waiting for it to finish."

// Collect picks up one async delegation's result by id.
//
// A delegation that is still running comes back with Pending set rather
// than blocking: turning a deliberately-async call into a blocking one at
// collect time would defeat the point of firing it in the background.
func (s *Service) Collect(ctx context.Context, delegationID, actorID string, isAdmin bool) (*CollectResult, error) {
	d, err := s.Repo.Get(ctx, delegationID)
	if err != nil {
		return nil, err
	}
	if !CanInterrupt(d, actorID, isAdmin) {
		return nil, ErrForbidden
	}

	out := &CollectResult{
		DelegationID: d.ID,
		Profile:      d.ProfileKey,
		Status:       d.Status,
		TurnsUsed:    d.TurnsUsed,
		TokensUsed:   d.TokensUsed,
		UserSteered:  d.UserSteered,
	}
	if !entity.IsTerminalDelegationStatus(d.Status) {
		out.Pending = true
		out.Note = pendingNote
		out.Progress = s.progressOf(d)
		out.LastReport = d.LastReport
		// Returns BEFORE MarkCollected, and must keep doing so. The
		// one-shot guard belongs to the final answer; marking a running row
		// collected would make its real result unreachable — the sub-agent
		// would finish, and the leader would be told it had already seen an
		// answer that was never handed over.
		return out, nil
	}

	out.Result = d.Result
	out.Note = collectNote(d)
	out.Envelope = EnvelopeOf(d)

	// Guarded: only the first collector gets it. A second call sees the
	// result again but is told it was already taken, so the leader does
	// not act on the same answer twice.
	if ok, err := s.Repo.MarkCollected(ctx, d.ID); err != nil {
		return nil, err
	} else if !ok {
		out.Note = "Already collected earlier — this is a repeat of a result you have seen. Do not act on it a second time."
	}
	return out, nil
}

// CollectPending lists async delegations for a leader that have finished
// but not been picked up yet.
func (s *Service) CollectPending(ctx context.Context, parentSessionID string) ([]CollectResult, error) {
	rows, err := s.Repo.ListCollectable(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]CollectResult, 0, len(rows))
	for _, d := range rows {
		item := CollectResult{
			DelegationID: d.ID, Profile: d.ProfileKey, Status: d.Status,
			TurnsUsed: d.TurnsUsed, TokensUsed: d.TokensUsed,
			UserSteered: d.UserSteered,
		}
		if entity.IsTerminalDelegationStatus(d.Status) {
			row := d
			item.Result = d.Result
			item.Note = collectNote(&row)
			item.Envelope = EnvelopeOf(&row)
		} else {
			row := d
			item.Pending = true
			item.Note = pendingNote
			item.Progress = s.progressOf(&row)
			item.LastReport = d.LastReport
		}
		out = append(out, item)
	}
	return out, nil
}

// progressOf reads what a running sub-agent has produced so far.
//
// Best-effort: a runner that cannot answer costs the peek, never the
// call. Only meaningful for a row still in flight — a terminal row's work
// is in Result, and reading the runner for one would return whatever
// happens to be left in a buffer for a process that has already gone.
func (s *Service) progressOf(d *entity.AgentDelegation) string {
	if s.Runner == nil || d == nil || entity.IsTerminalDelegationStatus(d.Status) {
		return ""
	}
	return strings.TrimSpace(s.Runner.PartialText(d.ChildSessionID, d.ChildAgent))
}

// collectNote explains a non-clean terminal status to the leader, reusing
// the same wording the synchronous path uses so a result reads the same
// either way.
func collectNote(d *entity.AgentDelegation) string {
	switch d.Status {
	case entity.DelegationInterrupted:
		return interruptNote
	case entity.DelegationStoppedMaxTurns:
		return "Stopped at its turn limit. The result above is partial."
	case entity.DelegationStoppedBudget:
		return "Stopped on budget. The result above is partial."
	case entity.DelegationFailed:
		if d.ErrorMsg != "" {
			return "Sub-agent failed: " + d.ErrorMsg
		}
		return "Sub-agent failed."
	}
	if d.UserSteered {
		return "A human sent guidance to this sub-agent while it worked, so the result reflects their steering as well as the role's own reasoning."
	}
	return ""
}
