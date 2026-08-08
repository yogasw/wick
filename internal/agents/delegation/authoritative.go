package delegation

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Which text is the sub-agent's actual answer.
//
// A run ends on the first turn that produces streamed text, and that text
// is recorded as the result. That inference is right for the ordinary
// case — one delegation, one question, one answer — and wrong the moment
// anything else makes the sub-agent speak.
//
// It does get spoken to. A peer can message it (DeliverInbox injects the
// message as a user turn), and the reply to THAT is what the accumulator
// holds when the turn closes. The row then records "Balasan terkirim ke
// @evidence-checker" as the answer to a question about currency codes,
// while the real findings sit untouched in the envelope. Worse, the async
// wake-up shows only the result, so a leader that never runs collect acts
// on the wrong text and has no way to notice.
//
// report_result is the sub-agent asserting "this is my answer". A guess
// derived from turn timing must not override an explicit claim.

// authoritativeResult picks the text recorded as a delegation's answer.
//
// The reported envelope wins over the last turn's prose whenever the two
// disagree, because only one of them was deliberate. Falls back to the
// streamed text when the sub-agent never reported — which is the common
// case and stays exactly as it was.
func (s *Service) authoritativeResult(ctx context.Context, row *entity.AgentDelegation, streamed string) string {
	streamed = strings.TrimSpace(streamed)
	if s == nil || s.Repo == nil || row == nil {
		return streamed
	}
	// Re-read: report_result is written by the sub-agent's own MCP call
	// during the run, so the in-memory row predates it.
	fresh, err := s.Repo.Get(context.WithoutCancel(ctx), row.ID)
	if err != nil {
		return streamed
	}
	env := EnvelopeOf(fresh)
	if env == nil || !env.Structured {
		return streamed
	}
	reported := strings.TrimSpace(env.Summary)
	if reported == "" || reported == streamed {
		return streamed
	}

	// Only override when the closing text was ADDRESSED ELSEWHERE.
	//
	// The two differing is normal and not a problem: a sub-agent that
	// reports structured findings and then closes with "done — see my
	// report" has said two true things, and the prose is its own summary
	// of its own work. Overriding there would throw away a human-readable
	// answer for no reason. The envelope is additive.
	//
	// It is only wrong when a peer messaged this sub-agent mid-run: the
	// reply to that message is what sits in the accumulator at turn end,
	// and recording it makes the delegation claim an answer to a question
	// nobody asked it. So the trigger is the delivery, not the wording —
	// guessing from the text would misfire on any legitimate answer that
	// happens to mention another agent.
	if !s.hadInboundDuringRun(ctx, fresh) {
		return streamed
	}

	log.Debug().
		Str("delegation", row.ID).
		Msg("delegation: a peer messaged this sub-agent mid-run; recording its reported result rather than the reply")

	return reported
}

// hadInboundDuringRun reports whether a peer's message was delivered into
// this delegation after it started.
//
// Read from the message rows rather than inferred from the text: a
// delivery is a fact wick recorded, while "does this prose look like a
// reply" is a guess that would misfire on any answer that mentions a
// colleague by name.
func (s *Service) hadInboundDuringRun(ctx context.Context, d *entity.AgentDelegation) bool {
	if d.Handle == "" || d.RootID == "" {
		return false
	}
	var n int64
	err := s.Repo.DB().WithContext(context.WithoutCancel(ctx)).
		Model(&entity.AgentMessage{}).
		Where("root_id = ? AND to_handle = ? AND delivered_at IS NOT NULL AND delivered_at >= ?",
			d.RootID, d.Handle, d.StartedAt).
		Count(&n).Error
	if err != nil {
		log.Debug().Err(err).Str("delegation", d.ID).
			Msg("delegation: inbound-message check failed; keeping the closing text")
		return false
	}
	return n > 0
}
