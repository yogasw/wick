package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// The checker loop.
//
// The split is the whole design. "Is every investigator finished?" and
// "did this round add evidence?" are queries — cheap, exact, and wrong
// often enough when a model answers them. "Do these findings hold
// together?" needs reading comprehension.
//
// So Go decides WHEN the checker runs, and the model decides only
// WHETHER the evidence stands. Only the second one pays for a spawn.

// Checker decisions.
const (
	DecisionConfirmed       = "confirmed"
	DecisionNeedMore        = "need_more_evidence"
	DecisionContradiction   = "contradiction"
	DecisionEscalateToHuman = "escalate_to_human"
)

// CheckerRoleKey is the role dispatched to judge the evidence.
const CheckerRoleKey = "evidence-checker"

// RoundState is the Go-side answer to "should the checker run now".
type RoundState struct {
	// Complete reports that every delegation in the current iteration has
	// reached a terminal status.
	Complete bool
	// NewEvidence is how many evidence rows this round added.
	NewEvidence int
	// DryRounds counts consecutive completed rounds that added nothing.
	DryRounds int
}

// OnDelegationFinished is the loop's single entry point.
//
// A no-op for a tree with no incident, which is most of them: an ordinary
// code review must not acquire an investigation loop because a sub-agent
// happened to quote a file.
func (s *Service) OnDelegationFinished(ctx context.Context, row *entity.AgentDelegation, verdict IntakeVerdict) {
	if s == nil || s.Repo == nil || row == nil {
		return
	}
	inc, err := s.IncidentForRoot(ctx, row.RootID)
	if err != nil || inc == nil {
		return
	}
	if entity.IsTerminalIncidentStatus(inc.Status) {
		return
	}

	// The checker's own completion is handled by decideNext, not by
	// starting another round about it.
	if row.ProfileKey == CheckerRoleKey {
		s.decideNext(ctx, inc, checkerDecisionOf(row))
		return
	}

	// A sub-agent that was asked to re-quote its evidence is not finished
	// with this round yet.
	if verdict.ReAsked {
		return
	}

	if reason := s.brakeTripped(ctx, inc); reason != "" {
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated, reason)
		return
	}

	state, err := s.roundState(ctx, inc)
	if err != nil || !state.Complete {
		return
	}

	lim := s.limits().normalize()
	if state.NewEvidence == 0 {
		if state.DryRounds >= lim.NoNewEvidenceRounds {
			s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
				fmt.Sprintf("%d consecutive rounds added no new evidence", state.DryRounds))
			return
		}
		// One empty round is normal — investigations routinely come up
		// blank once and land the next time.
		return
	}
	s.dispatchChecker(ctx, inc, verdict)
}

// roundState answers the two bookkeeping questions with queries.
func (s *Service) roundState(ctx context.Context, inc *entity.AgentIncident) (RoundState, error) {
	// Blocked rows are excluded for the same reason the queue excludes
	// them: a delegation parked on a synchronous child is waiting, not
	// working, and the child it waits on is counted in its own right.
	// Counting both would mean a round with any nesting never completes.
	var pending int64
	err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND iteration = ? AND status IN ? AND profile_key <> ? AND blocked = ?",
			inc.RootID, inc.Iteration,
			[]string{entity.DelegationQueued, entity.DelegationRunning}, CheckerRoleKey, false).
		Count(&pending).Error
	if err != nil {
		return RoundState{}, err
	}
	total, err := s.CountEvidence(ctx, inc.ID)
	if err != nil {
		return RoundState{}, err
	}
	st := RoundState{
		Complete:    pending == 0,
		NewEvidence: max(total-inc.EvidenceAtRoundStart, 0),
		DryRounds:   inc.DryRounds,
	}
	if st.Complete && st.NewEvidence == 0 {
		st.DryRounds = inc.DryRounds + 1
		if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
			Where("id = ?", inc.ID).Update("dry_rounds", st.DryRounds).Error; err != nil {
			log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: dry-round counter not stored")
		}
	}
	return st, nil
}

// brakeTripped reports why an investigation must stop, or "".
func (s *Service) brakeTripped(ctx context.Context, inc *entity.AgentIncident) string {
	lim := s.limits().normalize()
	if inc.Iteration >= lim.MaxIterations {
		return fmt.Sprintf("iteration cap reached (%d rounds)", lim.MaxIterations)
	}
	if !inc.CreatedAt.IsZero() {
		if elapsed := time.Since(inc.CreatedAt); elapsed > time.Duration(lim.MaxRuntimeMinutes)*time.Minute {
			// Wall clock catches what turn counting cannot: an agent that
			// is slow rather than chatty.
			return fmt.Sprintf("runtime cap reached (%d minutes)", lim.MaxRuntimeMinutes)
		}
	}
	return ""
}

// stopInvestigation writes a terminal status AND the reason for it.
//
// Every stop is explained: an investigation that ends without saying why
// is indistinguishable from one still running, which is the worst state
// for whoever is waiting on it.
func (s *Service) stopInvestigation(ctx context.Context, inc *entity.AgentIncident, status, reason string) {
	if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
		Where("id = ? AND status = ?", inc.ID, entity.IncidentInvestigating).
		Updates(map[string]any{"status": status, "stop_reason": reason}).Error; err != nil {
		log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: investigation stop not recorded")
		return
	}
	log.Info().Str("incident", inc.ID).Str("status", status).Str("reason", reason).
		Msg("delegation: investigation stopped")
}

// dispatchChecker sends the round's evidence to the judging role.
func (s *Service) dispatchChecker(ctx context.Context, inc *entity.AgentIncident, verdict IntakeVerdict) {
	task, err := s.checkerTask(ctx, inc, verdict)
	if err != nil {
		log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: checker task not built")
		return
	}
	_, err = s.Run(ctx, Request{
		ProfileKey:      CheckerRoleKey,
		Task:            task,
		RootID:          inc.RootID,
		Depth:           1,
		ProjectID:       inc.ProjectID,
		TriggeredBy:     inc.TriggeredBy,
		ParentSessionID: s.parentSessionFor(ctx, inc.RootID),
		Mode:            ModeAsync,
		DeliverySink:    SinkNone,
		MemoryMode:      MemorySummary,
	})
	if err != nil {
		// A refusal is recorded where a person will see it rather than
		// swallowed into a log nobody reads.
		s.noteOnIncident(ctx, inc, "evidence check could not start: "+err.Error())
	}
}

// checkerTask renders the round's material for the judging role.
func (s *Service) checkerTask(ctx context.Context, inc *entity.AgentIncident, verdict IntakeVerdict) (string, error) {
	ev, err := s.ListEvidence(ctx, inc.ID)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("Judge whether this investigation's evidence supports its findings.\n\n")
	if inc.Summary != "" {
		sb.WriteString("Current understanding: " + inc.Summary + "\n\n")
	}
	if h := decodeJSONList(inc.Hypotheses); len(h) > 0 {
		sb.WriteString("Hypotheses under test:\n- " + strings.Join(h, "\n- ") + "\n\n")
	}
	if verdict.Unsupported {
		sb.WriteString("NOTE: at least one agent reported findings with no evidence behind them. Weigh them accordingly.\n\n")
	}

	byKind := map[string][]entity.AgentEvidence{}
	order := []string{}
	for _, e := range ev {
		if _, seen := byKind[e.Kind]; !seen {
			order = append(order, e.Kind)
		}
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	sb.WriteString("Evidence collected so far:\n")
	for _, kind := range order {
		fmt.Fprintf(&sb, "\n[%s]\n", kind)
		for _, e := range byKind[kind] {
			fmt.Fprintf(&sb, "- (%s, via %s) %s\n", e.Source, e.Role, strings.TrimSpace(e.Excerpt))
		}
	}
	sb.WriteString("\nReport your verdict through report_result. Prefer need_more_evidence over a confident guess.\n")
	return sb.String(), nil
}

// checkerDecisionOf reads the verdict a checker reported, or nil.
func checkerDecisionOf(row *entity.AgentDelegation) *CheckerDecision {
	env := EnvelopeOf(row)
	if env == nil {
		return nil
	}
	return env.Checker
}

// decideNext acts on the checker's verdict.
func (s *Service) decideNext(ctx context.Context, inc *entity.AgentIncident, d *CheckerDecision) {
	// An unreadable verdict is NOT a pass. A checker that returned
	// nothing has told us only that it could not decide, and treating
	// that as approval is exactly the failure the checker exists to
	// prevent.
	if d == nil || strings.TrimSpace(d.Decision) == "" {
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			"the evidence check returned no decision")
		return
	}

	switch d.Decision {
	case DecisionConfirmed:
		if len(d.ConfirmedFindings) > 0 {
			summary := strings.Join(d.ConfirmedFindings, "\n- ")
			if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
				Where("id = ?", inc.ID).Update("summary", "- "+summary).Error; err != nil {
				log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: confirmed findings not stored")
			}
		}
		s.stopInvestigation(ctx, inc, entity.IncidentConfirmed, "the evidence supports the findings")

	case DecisionContradiction:
		s.recordList(ctx, inc, "next_actions", d.Contradictions)
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			"the sources contradict each other and a person has to decide which is right")

	case DecisionEscalateToHuman:
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			"the evidence check asked for a human")

	case DecisionNeedMore:
		s.startNextRound(ctx, inc, d)

	default:
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			"the evidence check returned an unrecognised decision: "+d.Decision)
	}
}

// startNextRound records what is missing and dispatches the follow-ups.
func (s *Service) startNextRound(ctx context.Context, inc *entity.AgentIncident, d *CheckerDecision) {
	lim := s.limits().normalize()
	if inc.Iteration+1 >= lim.MaxIterations {
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			fmt.Sprintf("iteration cap reached (%d rounds) with the evidence still incomplete", lim.MaxIterations))
		return
	}

	s.recordList(ctx, inc, "missing_evidence", d.MissingEvidence)

	total, err := s.CountEvidence(ctx, inc.ID)
	if err != nil {
		log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: round snapshot failed")
		return
	}
	next := inc.Iteration + 1
	if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
		Where("id = ?", inc.ID).Updates(map[string]any{
		"iteration":               next,
		"evidence_at_round_start": total,
	}).Error; err != nil {
		log.Warn().Err(err).Str("incident", inc.ID).Msg("delegation: round advance failed")
		return
	}

	if len(d.FollowupTasks) == 0 {
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			"more evidence is needed but the check named no way to get it")
		return
	}

	parent := s.parentSessionFor(ctx, inc.RootID)
	for _, t := range d.FollowupTasks {
		if strings.TrimSpace(t.Role) == "" || strings.TrimSpace(t.Task) == "" {
			continue
		}
		// Through the ordinary path: the loop gets no privileged route
		// around the governor or the queue.
		if _, err := s.Run(ctx, Request{
			ProfileKey:      t.Role,
			Task:            t.Task,
			Context:         t.Reason,
			RootID:          inc.RootID,
			Depth:           1,
			ProjectID:       inc.ProjectID,
			TriggeredBy:     inc.TriggeredBy,
			ParentSessionID: parent,
			Mode:            ModeAsync,
			DeliverySink:    SinkNone,
			MemoryMode:      MemorySummary,
		}); err != nil {
			s.noteOnIncident(ctx, inc, fmt.Sprintf("follow-up to %s refused: %s", t.Role, err.Error()))
		}
	}
}

// recordList replaces one of the incident's JSON list columns.
func (s *Service) recordList(ctx context.Context, inc *entity.AgentIncident, column string, items []string) {
	if len(items) == 0 {
		return
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return
	}
	if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
		Where("id = ?", inc.ID).Update(column, string(raw)).Error; err != nil {
		log.Warn().Err(err).Str("incident", inc.ID).Str("column", column).
			Msg("delegation: incident list not stored")
	}
}

// noteOnIncident appends a line to next_actions, which is where a person
// looks to find out what the loop could not do.
func (s *Service) noteOnIncident(ctx context.Context, inc *entity.AgentIncident, note string) {
	existing := decodeJSONList(inc.NextActions)
	s.recordList(ctx, inc, "next_actions", append(existing, note))
}

// parentSessionFor returns a session to hang a loop-started delegation
// off, so its rows appear in the same rail as everything else.
func (s *Service) parentSessionFor(ctx context.Context, rootID string) string {
	if root, err := s.Repo.Get(ctx, rootID); err == nil && root != nil {
		return root.ParentSessionID
	}
	return ""
}
