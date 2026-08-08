package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Sub-agents reporting their own progress.
//
// The pull half of supervision (Collect on a running row) answers "what
// is it doing" only when somebody asks. That leaves the supervisor
// choosing between checking constantly and finding out late.
//
// This is the push half: the sub-agent says where it is, and the leader
// is woken to hear it. Which means the interesting question is not how to
// deliver a report — it is WHEN a sub-agent should file one, and that is
// answered in the spawn preamble rather than here. See SupervisionBrief.

// progressMaxRunes bounds one note. A progress report is a couple of
// sentences; anything longer is the sub-agent narrating instead of
// working, and it lands in the leader's context either way.
const progressMaxRunes = 2000

// ProgressReport is one sub-agent progress note.
type ProgressReport struct {
	// Note is where the agent is now: what it just finished and what it is
	// moving to. Required — a report with no position is not a report.
	Note string
	// Done and Next are optional structure over the same thing, kept
	// separate so a leader scanning several reports can read intent
	// without parsing prose.
	Done string
	Next string
	// Blocked is what is stopping it, if anything.
	Blocked string
}

// ReportProgress records a sub-agent's progress note and wakes the agent
// supervising it.
//
// Waking rather than filing silently is the whole point. A note that sits
// unread until the next check-in adds nothing over reading the sub-agent's
// raw partial text, which Collect already exposes — the value of a report
// is that it arrives without having to be asked for.
//
// Only the latest note is kept. This is a "where are you now" field, not
// a log: the sub-agent's earlier positions are already in its transcript,
// and a growing history would be re-read on every supervision check.
func (s *Service) ReportProgress(ctx context.Context, childSessionID string, rep ProgressReport) (*ProgressResult, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("delegation service is not configured")
	}
	rep.Note = strings.TrimSpace(rep.Note)
	if rep.Note == "" {
		return nil, errors.New("note is required — say where you are and what you are moving to")
	}
	if runes := []rune(rep.Note); len(runes) > progressMaxRunes {
		return nil, fmt.Errorf("note too long (max %d characters) — report where you are, do not narrate", progressMaxRunes)
	}

	row, err := s.Repo.FindByChildSession(ctx, childSessionID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotASubAgent
	}
	// A finished delegation has nobody watching it work. Refused rather
	// than stored, because a note filed after the result was delivered
	// reaches a leader that has already moved on.
	if entity.IsTerminalDelegationStatus(row.Status) {
		return nil, fmt.Errorf("this delegation already finished (%s) — there is no work in progress to report on", row.Status)
	}

	text := formatProgress(row, rep)
	if err := s.Repo.SaveProgress(ctx, row.ID, composeStoredReport(rep)); err != nil {
		return nil, fmt.Errorf("record progress: %w", err)
	}

	// Delivery failure is logged, not returned. The note is already
	// durable on the row, so a wake that did not land costs the leader its
	// promptness — not the report. Returning an error here would tell a
	// sub-agent its report failed and invite it to spend another turn
	// re-filing something already recorded.
	delivered := false
	if s.Deliver != nil && row.ParentSessionID != "" {
		if err := s.Deliver.DeliverToSession(ctx, row.ParentSessionID, row.ParentAgent, text); err != nil {
			log.Warn().Err(err).
				Str("delegation", row.ID).
				Msg("delegation: progress note recorded but the leader could not be woken")
		} else {
			delivered = true
		}
	}

	return &ProgressResult{
		Recorded:  true,
		Delivered: delivered,
		Note:      progressAck(delivered),
	}, nil
}

// ErrNotASubAgent reports a progress call from a conversation that is not
// a delegation.
var ErrNotASubAgent = errors.New("only a sub-agent can report progress")

// ProgressResult is what the reporting sub-agent gets back.
type ProgressResult struct {
	Recorded bool `json:"recorded"`
	// Delivered reports whether the supervising agent was actually woken.
	// False means the note is on the record but nobody has been told yet —
	// it will be seen on the next supervision check.
	Delivered bool   `json:"delivered"`
	Note      string `json:"note,omitempty"`
}

// progressAck tells the sub-agent what to do next, which is: carry on.
//
// Said explicitly because the alternative is a model that files a report,
// sees a success, and treats the acknowledgement as a turn boundary worth
// waiting at. Nothing is coming back — the leader reads this on its own
// time.
func progressAck(delivered bool) string {
	if delivered {
		return "Recorded, and the agent supervising you has been told. Do NOT wait for a reply — " +
			"keep working. If it wants a change, it will message you."
	}
	return "Recorded. The supervising agent could not be woken right now, so it will see this on its next check. " +
		"Keep working; do not re-file this."
}

// composeStoredReport flattens a report into the single line kept on the
// row, so a supervisor reading last_report sees position without having
// to reassemble it from parts.
func composeStoredReport(rep ProgressReport) string {
	var b strings.Builder
	b.WriteString(rep.Note)
	if d := strings.TrimSpace(rep.Done); d != "" {
		b.WriteString("\nDone: " + d)
	}
	if n := strings.TrimSpace(rep.Next); n != "" {
		b.WriteString("\nNext: " + n)
	}
	if bl := strings.TrimSpace(rep.Blocked); bl != "" {
		b.WriteString("\nBlocked: " + bl)
	}
	return b.String()
}

// formatProgress renders a note for the leader's turn.
//
// Opens by naming the agent and marking this as progress, because it
// arrives as a user turn in the leader's session: without the frame, a
// mid-task note reads as the sub-agent's FINAL answer and the leader acts
// on half a job. The closing line is what stops a supervisor from
// answering every report — most need no reply at all.
func formatProgress(row *entity.AgentDelegation, rep ProgressReport) string {
	name := row.Handle
	if name == "" {
		name = row.ProfileKey
	}
	var b strings.Builder
	b.WriteString("@" + name + " is still working — progress report, NOT a final result.\n\n")
	b.WriteString(rep.Note)
	if d := strings.TrimSpace(rep.Done); d != "" {
		b.WriteString("\n\nDone so far:\n" + d)
	}
	if n := strings.TrimSpace(rep.Next); n != "" {
		b.WriteString("\n\nDoing next:\n" + n)
	}
	if bl := strings.TrimSpace(rep.Blocked); bl != "" {
		b.WriteString("\n\nBlocked by:\n" + bl)
	}
	b.WriteString("\n\nIf this is going the right way, say nothing and let it work. " +
		"To correct it, message @" + name + " — it is mid-task and will read that at its next turn boundary. " +
		"Do not collect: it has not finished.")
	return b.String()
}

// SupervisionBrief is what a supervised sub-agent is told about reporting,
// added to its spawn preamble.
//
// Criteria, deliberately, rather than a schedule. A turn is one call, not
// one unit of progress: five turns can be five file reads or one finished
// feature, so "report every N turns" produces notes that arrive at the
// wrong moment and say nothing worth waking anyone for.
//
// The last sentence is load-bearing, for the same reason interruptNote's
// is: models drop reporting instructions unless told what it costs.
const SupervisionBrief = "You are being supervised. File a progress note with the `progress` op when you reach a " +
	"point another agent would want to know about: a milestone finished, a plan that changed, or something blocking " +
	"you. It wakes the agent supervising you, so report MEANING, not activity — \"auth handler works, writing tests " +
	"now\", never \"read three files\". Say it when it happens and keep working; do not wait for a reply. A supervisor " +
	"who cannot see a wrong turn early will stop you to ask about it late."
