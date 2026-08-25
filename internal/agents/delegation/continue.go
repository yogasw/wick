package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Continuing a delegation instead of starting a new one.
//
// A delegation's child session id is derived from its UUID
// (childSessionIDFor), so a NEW delegation is always a new session with a
// blank transcript. That is the right default — one delegation is one
// question — but it makes the obvious supervision pattern impossible:
// a leader that reviews a sub-agent's work and wants it carried further
// had no way to say "keep going" without throwing away everything the
// sub-agent had learned.
//
// Continue reuses the row, and therefore the session, and therefore the
// transcript. What changes is only the instruction and the budget.

// ErrNotContinuable reports a delegation that cannot be continued because
// it has not stopped yet.
var ErrNotContinuable = errors.New("delegation is still running")

// ContinueRequest is one wick_agent_continue call.
type ContinueRequest struct {
	// DelegationID is the delegation to carry further.
	DelegationID string
	// Task is the NEXT instruction. It is not the original brief restated:
	// the sub-agent still holds its earlier work, so repeating the brief
	// invites it to start over.
	Task string
	// ExtraTurns / ExtraTokens are ADDED to what the row already allows,
	// not assigned over it. See applyContinuationBudget.
	ExtraTurns  int
	ExtraTokens int
	// Mode overrides background/foreground for this leg. Empty keeps
	// whatever the row already ran as.
	Mode string

	// ActorID is the human on whose authority this runs, and IsAdmin their
	// role. Checked with CanInterrupt: continuing someone else's sub-agent
	// is the same authority as stopping it.
	ActorID string
	IsAdmin bool
}

// Continue carries an existing delegation further in its own session.
//
// It deliberately does NOT go through Run. Run mints a delegation id and
// derives a child session from it, which is exactly what must not happen
// here — the whole point is to land on the session that already holds the
// sub-agent's transcript. execute() takes over from a row that already
// exists, so continuation is the same code path a queued delegation takes
// when its slot frees.
//
// The returned Result carries Resumed=false when the child's provider
// transcript could not be resumed. That is not a failure — the run
// proceeds in the same session either way — but a leader that believes a
// sub-agent remembers work it has actually forgotten will write a
// follow-up instruction that reads as gibberish to it.
func (s *Service) Continue(ctx context.Context, req ContinueRequest) (*Result, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("delegation service is not configured")
	}
	req.Task = strings.TrimSpace(req.Task)
	if req.Task == "" {
		return nil, errors.New("task is required — say what the sub-agent should do next")
	}

	row, err := s.Repo.Get(ctx, req.DelegationID)
	if err != nil {
		return nil, err
	}
	if !CanInterrupt(row, req.ActorID, req.IsAdmin) {
		return nil, ErrForbidden
	}

	// Still working: a second driver on the same session would interleave
	// with the turn already in flight. Steering a running sub-agent is
	// what message is for, and saying so beats a bare refusal — a model
	// told only "no" retries the identical call.
	if !entity.IsTerminalDelegationStatus(row.Status) {
		return nil, fmt.Errorf("%w: @%s is still working (%s). Use message to steer it, or stop it first",
			ErrNotContinuable, row.Handle, row.Status)
	}

	profile, err := s.Repo.GetProfileScoped(ctx, row.ProjectID, row.ProfileKey)
	if err != nil {
		return nil, fmt.Errorf("the %q role this delegation ran as is no longer available: %w", row.ProfileKey, err)
	}

	// Tag inheritance is re-resolved rather than reused: the human's tags
	// may have narrowed since the first leg, and a continuation must not
	// carry access they no longer hold.
	var userTags []string
	if s.Tags != nil && row.TriggeredBy != "" {
		userTags = s.Tags.GetUserFilterTagIDs(ctx, row.TriggeredBy)
	}
	effTags := EffectiveTags(userTags, profile)

	resumable := s.childIsResumable(row)
	priorStatus := row.Status

	row.Task = continuationTask(req.Task, priorStatus, resumable)
	// The caller's original `context` argument belongs to the first leg.
	// Replaying it here would re-deliver background the sub-agent has
	// already read and acted on.
	row.ContextText = ""
	applyContinuationBudget(row, req, profile, s.limits())
	if m := NormalizeMode(req.Mode, row.Mode); m != "" {
		row.Mode = m
		row.Detached = m == ModeAsync
	}

	// Guarded on the row still being terminal, so two continues racing on
	// the same delegation cannot both drive the session. The loser is told
	// what happened rather than handed a generic failure — it lost by a
	// hair, and its instruction may still be worth sending as a message.
	won, err := s.Repo.ReopenForContinue(ctx, row)
	if err != nil {
		return nil, fmt.Errorf("reopen delegation: %w", err)
	}
	if !won {
		return nil, fmt.Errorf("%w: @%s was continued by someone else a moment ago and is running again. "+
			"Use message to reach it", ErrNotContinuable, row.Handle)
	}
	row.Status = entity.DelegationRunning

	log.Debug().
		Str("delegation", row.ID).
		Str("child_session", row.ChildSessionID).
		Str("prior_status", priorStatus).
		Bool("resumable", resumable).
		Int("max_turns", row.MaxTurns).
		Msg("delegation: continuing in the existing session")

	res, err := s.execute(ctx, row, profile, effTags)
	if res != nil {
		res.Continued = true
		res.Resumed = resumable
		if !resumable {
			res.Note = joinNotes(res.Note, continuationLostNote)
		}
	}
	return res, err
}

// continuationLostNote is handed to a leader whose sub-agent could not
// resume its own transcript.
//
// Said in full rather than as a flag: the leader's next instruction is
// written on the assumption the sub-agent remembers, and the recovery
// (restate what it needs) is only obvious once the loss is spelled out.
const continuationLostNote = "This sub-agent could NOT resume its earlier transcript, so it is starting " +
	"from nothing in the same session. It does not remember its previous work — treat your instruction as " +
	"a fresh brief and restate whatever it needs, or re-delegate instead."

// continuationTask frames the next instruction for a sub-agent that is
// waking up inside its own finished session.
//
// Without the frame, a sub-agent resumed mid-transcript reads the new
// message as just another turn and frequently re-answers the ORIGINAL
// question. Naming why it stopped is what makes "keep going" actionable:
// a turn-exhausted agent should pick up where it left off, while an
// interrupted one must not assume its last direction was wanted.
func continuationTask(task, priorStatus string, resumable bool) string {
	if !resumable {
		// No transcript to refer back to: framing it as a continuation
		// would point at work the agent cannot see.
		return task
	}
	var lead string
	switch priorStatus {
	case entity.DelegationStoppedMaxTurns:
		lead = "You were stopped partway through because you ran out of turns, not because your work was wrong. " +
			"You have more turns now. Pick up exactly where you left off."
	case entity.DelegationStoppedBudget:
		lead = "You were stopped partway through because the token budget ran out, not because your work was wrong. " +
			"You have more budget now. Pick up where you left off, and prefer the cheaper route where you have a choice."
	case entity.DelegationInterrupted:
		lead = "A human stopped you mid-task. Do NOT assume the direction you were taking was wanted — " +
			"read the instruction below before carrying on."
	case entity.DelegationFailed:
		lead = "Your previous run ended in an error. Your earlier work is still here; " +
			"re-check the step that failed rather than starting over."
	default:
		lead = "You already finished one round of work in this session and it is still in your context. " +
			"This is a follow-up, not a new task — build on what you did, do not start over."
	}
	return lead + "\n\n" + task
}

// applyContinuationBudget raises the row's caps for another leg.
//
// Added, never assigned. The caps are absolute — await compares them
// against a turn count seeded from TurnsUsed — so a continuation that
// merely re-set MaxTurns to the profile default would hand a
// turn-exhausted sub-agent a budget it has already spent, and it would
// stop again on its first turn.
func applyContinuationBudget(row *entity.AgentDelegation, req ContinueRequest, profile *entity.AgentProfile, lim Limits) {
	lim = lim.normalize()

	extraTurns := req.ExtraTurns
	if extraTurns <= 0 {
		// No number asked for: grant another full allowance, which is what
		// "continue" means to a caller that did not want to think about
		// budgets.
		extraTurns = EffectiveMaxTurns(0, profile, lim)
	}
	row.MaxTurns = row.TurnsUsed + clampInt(extraTurns, 1, lim.MaxTurnsCap)

	// A row with no token cap stays uncapped: the per-tree budget is what
	// bounds it, and inventing a ceiling here would tighten a delegation
	// that was deliberately left open.
	if row.MaxTokens > 0 {
		extraTokens := req.ExtraTokens
		if extraTokens <= 0 {
			extraTokens = EffectiveMaxTokens(0, profile, lim)
		}
		row.MaxTokens = row.TokensUsed + extraTokens
		if lim.MaxTokensPerDelegation > 0 {
			// The ceiling bounds one LEG, not the lifetime of a delegation
			// that has been continued a dozen times.
			if cap := row.TokensUsed + lim.MaxTokensPerDelegation; row.MaxTokens > cap {
				row.MaxTokens = cap
			}
		}
	}
}

// childIsResumable reports whether the sub-agent's provider transcript can
// actually be picked up again.
//
// Best-effort and deliberately optimistic on a missing prober: a wiring
// that cannot answer must not make every continuation announce amnesia it
// has no evidence for.
func (s *Service) childIsResumable(row *entity.AgentDelegation) bool {
	if s.Resumable == nil || row == nil {
		return true
	}
	return s.Resumable(row.ChildSessionID, row.ChildAgent)
}

func joinNotes(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if hi > 0 && n > hi {
		return hi
	}
	return n
}
