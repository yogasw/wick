package delegation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// recordingRunner remembers which roles were spawned, so a test can
// assert what the loop decided to do rather than what it logged.
type recordingRunner struct {
	started []string
}

func (f *recordingRunner) EnsureChildSession(context.Context, string, string, string, string) error {
	return nil
}
func (f *recordingRunner) StartAgent(_ context.Context, spec ChildSpec) error {
	f.started = append(f.started, spec.Profile.Key)
	return nil
}
func (f *recordingRunner) KillAgent(string, string) error    { return nil }
func (f *recordingRunner) PartialText(string, string) string { return "" }

// loopService builds a service ready to run the checker loop.
func loopService(t *testing.T) (*Service, *Repo, *recordingRunner) {
	t.Helper()
	s, r := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "ok"},
		{Type: event.Done},
	}})
	run := &recordingRunner{}
	s.Runner = run
	s.Waker = &fakeWaker{}
	s.Steerer = &recordingSteerer{}
	// Room for the loop's own dispatches.
	lim := s.Limits
	lim.MaxParallel = 4
	s.Limits = lim
	seedProfile(t, r, CheckerRoleKey)
	seedProfile(t, r, "log-investigator")
	return s, r, run
}

// loopIncident seeds a tree with an incident mid-investigation.
func loopIncident(t *testing.T, s *Service, r *Repo) *entity.AgentIncident {
	t.Helper()
	ctx := context.Background()
	root := seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	root.Handle = entity.LeaderHandle
	root.Blocked = true
	if err := r.SaveDelegationForTest(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	inc, err := s.EnsureForRoot(ctx, "root-1")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return inc
}

// finishedInvestigator seeds an investigator that already finished this
// round, optionally with evidence in the pool.
func finishedInvestigator(t *testing.T, s *Service, r *Repo, id string, withEvidence bool) *entity.AgentDelegation {
	t.Helper()
	ctx := context.Background()
	row := seedDelegation(t, r, id, "root-1", entity.DelegationDone, 1)
	row.ProfileKey = "log-investigator"
	row.Handle = "log-investigator-" + id
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	if withEvidence {
		if _, err := s.IngestEvidence(ctx, row, &ResultEnvelope{Evidence: []Evidence{
			{Kind: EvidenceLog, Source: "loki: " + id, Excerpt: "401 " + id},
		}}); err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
	}
	return row
}

// A round with work still in flight is not a round.
func TestRoundIncompleteWhileOneStillRuns(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)

	done := finishedInvestigator(t, s, r, "d1", true)
	running := seedDelegation(t, r, "d2", "root-1", entity.DelegationRunning, 0)
	running.ProfileKey = "log-investigator"
	if err := r.SaveDelegationForTest(ctx, running); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s.OnDelegationFinished(ctx, done, IntakeVerdict{})

	if len(run.started) != 0 {
		t.Fatalf("the checker ran with work still in flight: %v", run.started)
	}
}

// A completed round that established something is exactly when a
// judgement is worth paying for.
func TestCompletedRoundWithNewEvidenceDispatchesTheChecker(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)

	row := finishedInvestigator(t, s, r, "d1", true)
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	if len(run.started) != 1 || run.started[0] != CheckerRoleKey {
		t.Fatalf("started %v, want exactly one %s", run.started, CheckerRoleKey)
	}
}

// One empty round is normal — investigations routinely come up blank
// once and land the next time. Two in a row is a dead end.
func TestTwoDryRoundsStopWithEscalated(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	inc := loopIncident(t, s, r)

	first := finishedInvestigator(t, s, r, "d1", false)
	s.OnDelegationFinished(ctx, first, IntakeVerdict{})
	if len(run.started) != 0 {
		t.Fatalf("a single dry round dispatched %v", run.started)
	}
	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Status != entity.IncidentInvestigating {
		t.Fatalf("one dry round stopped the investigation: %q", got.Status)
	}

	second := finishedInvestigator(t, s, r, "d2", false)
	s.OnDelegationFinished(ctx, second, IntakeVerdict{})

	got, _ = s.IncidentForRoot(ctx, inc.RootID)
	if got.Status != entity.IncidentEscalated {
		t.Fatalf("status = %q, want escalated", got.Status)
	}
	// A stop that does not say why is indistinguishable from one still
	// running.
	if !strings.Contains(got.StopReason, "no new evidence") {
		t.Fatalf("stop reason = %q", got.StopReason)
	}
}

// seedCheckerResult seeds a finished checker carrying a verdict.
func seedCheckerResult(t *testing.T, r *Repo, d *CheckerDecision) *entity.AgentDelegation {
	t.Helper()
	ctx := context.Background()
	row := seedDelegation(t, r, "chk", "root-1", entity.DelegationDone, 1)
	row.ProfileKey = CheckerRoleKey
	row.Handle = CheckerRoleKey
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed checker: %v", err)
	}
	env := &ResultEnvelope{Summary: "verdict", Confidence: ConfidenceHigh, Structured: true, Checker: d}
	if err := r.SaveResultJSON(ctx, row.ID, env); err != nil {
		t.Fatalf("save verdict: %v", err)
	}
	fresh, _ := r.Get(ctx, row.ID)
	return fresh
}

func TestConfirmedVerdictClosesTheInvestigation(t *testing.T) {
	s, r, _ := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)

	row := seedCheckerResult(t, r, &CheckerDecision{
		Decision:          DecisionConfirmed,
		ConfirmedFindings: []string{"the retry path drops the signature header"},
	})
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Status != entity.IncidentConfirmed {
		t.Fatalf("status = %q, want confirmed", got.Status)
	}
	if !strings.Contains(got.Summary, "signature header") {
		t.Fatalf("confirmed findings were not recorded: %q", got.Summary)
	}
}

// An unreadable verdict is not a pass. That is the failure the checker
// exists to prevent, so it must not be the checker's own failure mode.
func TestAnEmptyVerdictEscalates(t *testing.T) {
	for name, d := range map[string]*CheckerDecision{
		"nil":     nil,
		"blank":   {Decision: "  "},
		"unknown": {Decision: "probably fine"},
	} {
		t.Run(name, func(t *testing.T) {
			s, r, _ := loopService(t)
			ctx := context.Background()
			loopIncident(t, s, r)

			row := seedCheckerResult(t, r, d)
			s.OnDelegationFinished(ctx, row, IntakeVerdict{})

			got, _ := s.IncidentForRoot(ctx, "root-1")
			if got.Status != entity.IncidentEscalated {
				t.Fatalf("status = %q, want escalated", got.Status)
			}
			if got.StopReason == "" {
				t.Fatal("escalated with no reason")
			}
		})
	}
}

func TestNeedMoreEvidenceDispatchesTheFollowups(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)

	row := seedCheckerResult(t, r, &CheckerDecision{
		Decision:        DecisionNeedMore,
		MissingEvidence: []string{"the signature header from a failing request"},
		FollowupTasks: []NextTask{{
			Role: "log-investigator", Task: "search for signature_invalid between 10:00 and 10:30",
			Reason: "the 401s do not yet prove a signature mismatch",
		}},
	})
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	if len(run.started) != 1 || run.started[0] != "log-investigator" {
		t.Fatalf("started %v, want the follow-up investigator", run.started)
	}
	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Iteration != 1 {
		t.Fatalf("iteration = %d, want 1", got.Iteration)
	}
	var missing []string
	_ = json.Unmarshal([]byte(got.MissingEvidence), &missing)
	if len(missing) != 1 {
		t.Fatalf("missing evidence not recorded: %q", got.MissingEvidence)
	}
	if got.Status != entity.IncidentInvestigating {
		t.Fatalf("status = %q, want the investigation still open", got.Status)
	}
}

// "More evidence is needed" with no way to get it is a dead end, not a
// round.
func TestNeedMoreWithNoFollowupsEscalates(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)

	row := seedCheckerResult(t, r, &CheckerDecision{Decision: DecisionNeedMore})
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	if len(run.started) != 0 {
		t.Fatalf("started %v, want nothing", run.started)
	}
	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Status != entity.IncidentEscalated {
		t.Fatalf("status = %q, want escalated", got.Status)
	}
}

func TestIterationCapStopsTheLoop(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	inc := loopIncident(t, s, r)
	lim := s.Limits
	lim.MaxIterations = 2
	s.Limits = lim
	if err := r.DB().Model(&entity.AgentIncident{}).
		Where("id = ?", inc.ID).Update("iteration", 1).Error; err != nil {
		t.Fatalf("seed iteration: %v", err)
	}

	row := seedCheckerResult(t, r, &CheckerDecision{
		Decision:      DecisionNeedMore,
		FollowupTasks: []NextTask{{Role: "log-investigator", Task: "one more look"}},
	})
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	if len(run.started) != 0 {
		t.Fatalf("the cap was ignored: %v", run.started)
	}
	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Status != entity.IncidentEscalated || !strings.Contains(got.StopReason, "iteration cap") {
		t.Fatalf("status = %q, reason = %q", got.Status, got.StopReason)
	}
}

// A refusal must land where a person will see it, not in a log nobody
// reads.
func TestAFollowupRefusalIsRecordedOnTheIncident(t *testing.T) {
	s, r, _ := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)
	// Burn the tree's turn budget so the governor refuses the follow-up.
	lim := s.Limits
	lim.RootBudget = 1
	s.Limits = lim
	spender := seedDelegation(t, r, "spender", "root-1", entity.DelegationDone, 5)
	_ = spender

	row := seedCheckerResult(t, r, &CheckerDecision{
		Decision:      DecisionNeedMore,
		FollowupTasks: []NextTask{{Role: "log-investigator", Task: "one more look"}},
	})
	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	got, _ := s.IncidentForRoot(ctx, "root-1")
	if !strings.Contains(got.NextActions, "refused") {
		t.Fatalf("the refusal was swallowed: %q", got.NextActions)
	}
}

// Wall clock catches what turn counting cannot: an agent that is slow
// rather than chatty.
func TestRuntimeCapStopsAnInvestigationAndKeepsWhatItFound(t *testing.T) {
	s, r, _ := loopService(t)
	ctx := context.Background()
	inc := loopIncident(t, s, r)
	lim := s.Limits
	lim.MaxRuntimeMinutes = 1
	s.Limits = lim

	finishedInvestigator(t, s, r, "d1", true)
	// Backdate the incident past the cap.
	if err := r.DB().Model(&entity.AgentIncident{}).Where("id = ?", inc.ID).
		Update("created_at", time.Now().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	NewDelegationSweeper(s, time.Second).Pass(ctx)

	got, _ := s.IncidentForRoot(ctx, "root-1")
	if got.Status != entity.IncidentEscalated {
		t.Fatalf("status = %q, want escalated", got.Status)
	}
	if !strings.Contains(got.StopReason, "runtime cap") {
		t.Fatalf("stop reason = %q", got.StopReason)
	}
	// Partial work survives: an investigation that ran out of time still
	// established whatever it established.
	rows, _ := s.ListEvidence(ctx, inc.ID)
	if len(rows) == 0 {
		t.Fatal("the runtime cap discarded the evidence already collected")
	}
}

// A result that landed while the leader was away is nudged once, not
// once per sweep.
func TestStalledCollectIsNudgedExactlyOnce(t *testing.T) {
	s, r, _ := loopService(t)
	ctx := context.Background()
	del := &recordingDeliverer{}
	s.Deliver = del

	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	row.Mode = ModeAsync
	row.DeliverySink = SinkSession
	row.Handle = "log-investigator"
	ended := time.Now().Add(-time.Hour)
	row.EndedAt = &ended
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := NewDelegationSweeper(s, time.Second)
	sw.Pass(ctx)
	sw.Pass(ctx)

	if got := del.sessionDeliveries(); len(got) != 1 {
		t.Fatalf("nudges = %d, want exactly 1", len(got))
	}
}

// A result the leader already picked up needs no nudge.
func TestCollectedResultIsNotNudged(t *testing.T) {
	s, r, _ := loopService(t)
	ctx := context.Background()
	del := &recordingDeliverer{}
	s.Deliver = del

	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	row.Mode = ModeAsync
	row.DeliverySink = SinkSession
	row.Collected = true
	ended := time.Now().Add(-time.Hour)
	row.EndedAt = &ended
	if err := r.SaveDelegationForTest(ctx, row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	NewDelegationSweeper(s, time.Second).Pass(ctx)

	if got := del.sessionDeliveries(); len(got) != 0 {
		t.Fatalf("nudged a collected result: %v", got)
	}
}

// An ordinary delegation tree must not acquire an investigation loop.
func TestNoIncidentMeansNoLoop(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	row := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)

	s.OnDelegationFinished(ctx, row, IntakeVerdict{})

	if len(run.started) != 0 {
		t.Fatalf("a plain delegation tree started %v", run.started)
	}
}

// A sub-agent that was asked to re-quote its evidence has not finished
// its part of the round.
func TestAReAskedAgentDoesNotCloseTheRound(t *testing.T) {
	s, r, run := loopService(t)
	ctx := context.Background()
	loopIncident(t, s, r)
	row := finishedInvestigator(t, s, r, "d1", true)

	s.OnDelegationFinished(ctx, row, IntakeVerdict{ReAsked: true})

	if len(run.started) != 0 {
		t.Fatalf("the round closed while an agent was still being asked: %v", run.started)
	}
}
