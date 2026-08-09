package delegation

import (
	"context"
	"strings"
	"time"

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
	// LastReportAt is when that note was filed, RFC3339 UTC.
	//
	// Without it a supervisor cannot tell a report filed ten seconds ago
	// from one filed ten minutes ago, and those mean opposite things: the
	// first is a healthy run, the second is one that has gone quiet. The
	// text alone reads identically in both cases.
	LastReportAt string `json:"last_report_at,omitempty"`
	// Envelope is the sub-agent's answer as typed fields, when it has one.
	Envelope *ResultEnvelope `json:"envelope,omitempty"`
}

// pendingNote is what a leader is told about a delegation still working.
//
// Written from what this particular reply actually carries, not from what
// the shape can carry. The previous text was one fixed sentence promising
// "the progress below" — and `progress` is empty whenever the sub-agent
// is between turns, which for a respawn-per-turn provider is most of the
// time. A note pointing at a field that is not there teaches a leader to
// stop reading notes.
//
// It also no longer recommends `message` unconditionally. That op waits
// for the recipient's turn boundary and times out often enough that
// naming it as THE way to intervene sends a supervisor down a path that
// frequently fails. Stop is named instead: it is the one control that
// always applies to a running sub-agent.
func pendingNote(progress, lastReport string) string {
	var b strings.Builder
	b.WriteString("Still working — this is a progress check, not the answer. ")
	b.WriteString("The result arrives on its own; do not sit in a loop waiting for it here. ")

	switch {
	case lastReport != "":
		b.WriteString("`last_report` is where this sub-agent says it has got to. ")
	case progress != "":
		b.WriteString("`progress` is a scrape of its in-flight turn — a partial thought, not a conclusion. ")
	default:
		b.WriteString("It has not reported anything yet, so there is nothing to judge: either it is early, " +
			"or it was not asked to report (delegate with supervised=true for work you intend to watch). ")
	}

	if progress != "" || lastReport != "" {
		b.WriteString("If it is going the wrong way, stop it and start again with a corrected brief — " +
			"message reaches it only at its next turn boundary and may not arrive in time.")
	}
	return strings.TrimSpace(b.String())
}

// formatStamp renders a nullable timestamp for the wire, or "" when the
// event never happened.
func formatStamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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
		out.Progress = s.progressOf(d)
		out.LastReport = strings.TrimSpace(d.LastReport)
		out.LastReportAt = formatStamp(d.LastReportAt)
		out.Note = pendingNote(out.Progress, out.LastReport)
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
			row := d
			item.Pending = true
			item.Progress = s.progressOf(&row)
			item.LastReport = strings.TrimSpace(d.LastReport)
			item.LastReportAt = formatStamp(d.LastReportAt)
			item.Note = pendingNote(item.Progress, item.LastReport)
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
