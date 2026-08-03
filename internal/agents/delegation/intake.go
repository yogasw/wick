package delegation

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// The intake gate.
//
// "Validate the sub-agent's work" is two different jobs, and putting both
// in the checker was the mistake. Whether a string is empty is not a
// judgement: paying for a spawn to answer it is expensive, occasionally
// wrong, and lets unsourced claims into the evidence pool where a model
// then has to argue them back out.
//
// So: Go decides whether a report is well-formed, and the checker decides
// whether it is TRUE.

// IntakeVerdict is what the mechanical gate decided about one envelope.
type IntakeVerdict struct {
	// Accepted are the evidence items that may be stored.
	Accepted []Evidence
	// Dropped are items that failed twice and were discarded.
	Dropped []Evidence
	// ReAsked reports that a follow-up was sent this time.
	ReAsked bool
	// Unsupported marks findings asserted with no evidence behind them.
	// Not a rejection — the checker is told, and weighs them accordingly.
	Unsupported bool
	// Unstructured marks an envelope reconstructed from prose.
	Unstructured bool
}

// GateEnvelope validates an envelope mechanically, before its evidence
// reaches the incident.
//
// Calls no model. One re-ask per delegation, ever: unbounded re-asking is
// how a turn budget disappears into an argument about formatting.
func (s *Service) GateEnvelope(ctx context.Context, row *entity.AgentDelegation, env *ResultEnvelope) IntakeVerdict {
	v := IntakeVerdict{}
	if row == nil || env == nil {
		return v
	}
	v.Unstructured = !env.Structured

	var bad []string
	for i, e := range env.Evidence {
		switch {
		case strings.TrimSpace(e.Source) == "":
			bad = append(bad, fmt.Sprintf("item %d has no source", i+1))
			v.Dropped = append(v.Dropped, e)
		case strings.TrimSpace(e.Excerpt) == "":
			bad = append(bad, fmt.Sprintf("item %d has no excerpt", i+1))
			v.Dropped = append(v.Dropped, e)
		default:
			v.Accepted = append(v.Accepted, e)
		}
	}

	// Findings with nothing quoted behind them are not dropped — the
	// agent may be right — but the checker is told, so a plausible
	// sentence is not weighed like a verified one.
	v.Unsupported = len(env.Findings) > 0 && len(v.Accepted) == 0

	if len(bad) == 0 {
		return v
	}
	if row.IntakeReasked {
		// Second failure: keep the well-formed items, discard the rest,
		// and record it rather than arguing further.
		log.Info().Str("delegation", row.ID).Int("dropped", len(v.Dropped)).
			Msg("delegation: evidence dropped after a re-ask went unanswered")
		return v
	}

	// First failure: ask once, naming exactly what was missing.
	if err := s.reAskForEvidence(ctx, row, bad); err != nil {
		log.Debug().Err(err).Str("delegation", row.ID).
			Msg("delegation: evidence re-ask not delivered")
		return v
	}
	v.ReAsked = true
	// Dropped items are held back this round, not discarded: the agent
	// still has a chance to quote them properly.
	v.Dropped = nil
	return v
}

// reAskForEvidence sends the one follow-up a malformed report earns.
func (s *Service) reAskForEvidence(ctx context.Context, row *entity.AgentDelegation, problems []string) error {
	if err := s.Repo.MarkIntakeReasked(ctx, row.ID); err != nil {
		return err
	}
	row.IntakeReasked = true
	body := "Your reported evidence is not usable as it stands: " +
		strings.Join(problems, ", ") + ". " +
		"Quote the source and the excerpt for each — a claim nobody else can check cannot be recorded — or drop the claim. " +
		"Call report_result again with the corrected list. This is asked once."
	_, err := s.SendMessage(ctx, SendInput{
		RootID: row.RootID, FromHandle: entity.LeaderHandle,
		ToHandle: row.Handle, Body: body, Kind: entity.MessageTell,
	})
	return err
}
