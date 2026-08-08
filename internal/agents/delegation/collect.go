package delegation

import (
	"context"

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
	// Envelope is the sub-agent's answer as typed fields, when it has one.
	Envelope *ResultEnvelope `json:"envelope,omitempty"`
}

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
		out.Note = "Still running. Carry on with other work and collect again later; do not block on this."
		return out, nil
	}

	out.Result = d.Result
	out.Note = collectNote(d)
	out.Envelope = EnvelopeOf(d)

	// Guarded: only the first collector gets it. A second call sees the
	// result again but is told it was already handed over, so the leader
	// does not act on the same answer twice.
	//
	// "Already" does NOT imply this leader ran collect before. The common
	// case is the opposite: delivery_sink=session marks the row collected
	// when it wakes the leader WITH the result (see deliver), so the very
	// first manual collect after an async run lands here. The wording has
	// to survive that reading — a note saying "you have seen this" to an
	// agent that has not would be read as "this is stale, discard it", and
	// a correct result would be thrown away.
	if ok, err := s.Repo.MarkCollected(ctx, d.ID); err != nil {
		return nil, err
	} else if !ok {
		out.Note = "This result was already handed over once — most likely delivered to you when this sub-agent " +
			"finished. The content above is the real, complete result: use it if you have not acted on it yet, " +
			"but do not act on it a second time if you already have."
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
			item.Pending = true
		}
		out = append(out, item)
	}
	return out, nil
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
