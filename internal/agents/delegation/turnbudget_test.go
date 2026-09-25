package delegation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

/* The turn budget a sub-agent is allowed to see.

   Sub-agents were spawned with their role prompt and nothing else, then
   killed mid-action by a cap they were never told about — twice leaving a
   tree that would not build. These tests hold the three halves of the
   fix: the child knows its allowance, the leader is warned before the cap
   lands, and the partial result says how to carry the work on. */

// The cap is enforced by the harness; the sub-agent has to be able to see
// it. The numbers go in beside the role prompt, not instead of it.
func TestChildAddonCarriesBothRolePromptAndTurnBudget(t *testing.T) {
	layout := layoutForTest(t)
	const childID = "sub-budget-1"
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: childID}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	profile := &entity.AgentProfile{
		Key: "reviewer", Provider: "claude",
		SystemPrompt: "You review code and return findings ranked by severity.",
	}
	if err := applyRolePrompt(layout, childID, profile, 0, 12); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sess, err := session.Load(layout, childID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	addon := sess.Meta.SystemAddon
	if !strings.Contains(addon, profile.SystemPrompt) {
		t.Fatalf("role prompt lost: %q", addon)
	}
	if !strings.Contains(addon, "12") {
		t.Fatalf("the addon never names the turn cap: %q", addon)
	}
	// The consequence is the part that changes behaviour. A number with no
	// stated cost reads as trivia.
	if !strings.Contains(addon, "KILLED MID-ACTION") {
		t.Fatalf("the addon does not say what running out costs: %q", addon)
	}
}

// A continuation re-runs the spawn path on the same session with a RAISED
// absolute cap. Appending each time would leave two contradictory budgets
// in one prompt, and the number that matters to a continued agent is what
// is left, not the total.
func TestTurnBudgetIsReplacedNotStackedOnContinuation(t *testing.T) {
	layout := layoutForTest(t)
	const childID = "sub-budget-2"
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: childID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	profile := &entity.AgentProfile{Key: "reviewer", Provider: "claude", SystemPrompt: "role prompt"}

	if err := applyRolePrompt(layout, childID, profile, 0, 12); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := applyRolePrompt(layout, childID, profile, 12, 24); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	sess, _ := session.Load(layout, childID)
	addon := sess.Meta.SystemAddon
	if n := strings.Count(addon, turnBudgetHeading); n != 1 {
		t.Fatalf("budget block appears %d times, want exactly 1: %q", n, addon)
	}
	if !strings.Contains(addon, "24") || !strings.Contains(addon, "12 already spent") {
		t.Fatalf("a continued child is not told what it has left: %q", addon)
	}
	if strings.Count(addon, "role prompt") != 1 {
		t.Fatalf("role prompt duplicated or lost: %q", addon)
	}
}

// A role with no prompt must still learn its budget — and must not lose
// an addon the session carries from elsewhere.
func TestBudgetIsAddedWithoutBlankingAnExistingAddon(t *testing.T) {
	layout := layoutForTest(t)
	const childID = "sub-budget-3"
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: childID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.SetSystemAddon(layout, childID, "pre-existing"); err != nil {
		t.Fatalf("seed addon: %v", err)
	}

	if err := applyRolePrompt(layout, childID, &entity.AgentProfile{Key: "bare", Provider: "claude"}, 0, 8); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sess, _ := session.Load(layout, childID)
	if !strings.Contains(sess.Meta.SystemAddon, "pre-existing") {
		t.Fatalf("existing addon was blanked: %q", sess.Meta.SystemAddon)
	}
	if !strings.Contains(sess.Meta.SystemAddon, turnBudgetHeading) {
		t.Fatalf("budget block missing: %q", sess.Meta.SystemAddon)
	}
}

// The warning has to land with turns still to spend — one that fires on
// the turn that kills the agent tells the leader nothing new.
func TestTurnWarnThresholdLeavesRoomToAct(t *testing.T) {
	for _, tc := range []struct{ cap, want int }{
		{cap: 50, want: 40},
		{cap: 12, want: 10},
		{cap: 10, want: 8},
		{cap: 5, want: 4},
		{cap: 2, want: 1},
		// Nothing useful to say about a one-turn delegation.
		{cap: 1, want: 0},
		{cap: 0, want: 0},
	} {
		if got := turnWarnThreshold(tc.cap); got != tc.want {
			t.Fatalf("turnWarnThreshold(%d) = %d, want %d", tc.cap, got, tc.want)
		}
		if tc.want > 0 && tc.want >= tc.cap {
			t.Fatalf("threshold %d for cap %d leaves no turn to act in", tc.want, tc.cap)
		}
	}
}

// A supervised sub-agent burning through its budget must reach its leader
// BEFORE the cap, once — the only moment the leader can still steer it,
// and often enough that the note keeps being read.
func TestSupervisedRunWarnsTheLeaderOnceBeforeTheTurnCap(t *testing.T) {
	// Five silent turn boundaries: no text, so nothing closes the run
	// early and the cap is what ends it.
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done},
		{Type: event.Done}, {Type: event.Done},
	}}
	runner := &fakeRunner{partial: "half an answer"}
	s, r, _ := runService(t, stream, runner)
	deliver := &nudgeDeliverer{}
	s.Deliver = deliver
	runner.onStart = func(spec ChildSpec) {
		row, err := r.FindByChildSession(context.Background(), spec.SessionID)
		if err != nil || row == nil {
			t.Errorf("row not found: %v", err)
			return
		}
		_ = r.SaveProgress(context.Background(), row.ID, "step 3 of 9: patched the handler")
	}

	req := baseReq()
	req.Supervised = true
	req.MaxTurns = 5
	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("status = %q, want the run to end on its turn cap", res.Status)
	}

	sent := deliver.delivered()
	if len(sent) != 1 {
		t.Fatalf("want exactly one warning, got %d: %v", len(sent), sent)
	}
	if !strings.Contains(sent[0], "4/5") {
		t.Fatalf("warning does not say how much is left: %q", sent[0])
	}
	if !strings.Contains(sent[0], "step 3 of 9") {
		t.Fatalf("warning does not carry the last known position: %q", sent[0])
	}
}

// A delegation that asked for no supervision must not be woken by this.
// The warning rides the supervision channel; sending it anyway would put
// a note in the context of a leader that opted out of reporting.
func TestUnsupervisedRunDoesNotWarnTheLeader(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done},
		{Type: event.Done}, {Type: event.Done},
	}}
	runner := &fakeRunner{partial: "half an answer"}
	s, _, _ := runService(t, stream, runner)
	deliver := &nudgeDeliverer{}
	s.Deliver = deliver

	req := baseReq()
	req.MaxTurns = 5
	if _, err := s.Run(context.Background(), req); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sent := deliver.delivered(); len(sent) != 0 {
		t.Fatalf("an unsupervised delegation must not wake its leader: %v", sent)
	}
}

// A turn-exhausted result is a handover, not an answer. The leader needs
// the id, both numbers and the partial marker in TYPED fields — a status
// string it has to know the members of is not enough, and re-delegating
// from scratch pays again for context the sub-agent still holds.
func TestMaxTurnsResultCarriesWhatAContinuationNeeds(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done},
	}}
	runner := &fakeRunner{partial: "got as far as the migration"}
	s, _, _ := runService(t, stream, runner)

	req := baseReq()
	req.MaxTurns = 3
	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("status = %q, want stopped_max_turns", res.Status)
	}
	if !res.Partial {
		t.Fatal("a turn-exhausted result must be marked partial")
	}
	if res.MaxTurns != 3 || res.TurnsUsed != 3 {
		t.Fatalf("turns = %d/%d, want 3/3", res.TurnsUsed, res.MaxTurns)
	}
	if res.DelegationID == "" || !strings.Contains(res.Note, res.DelegationID) {
		t.Fatalf("note does not name the delegation to continue: %q", res.Note)
	}
	if !strings.Contains(res.Note, "continue") {
		t.Fatalf("note does not point at the continuation path: %q", res.Note)
	}
}

// The async half of the same contract: a leader that picks the result up
// with collect reads the same fields as one that ran it synchronously.
func TestCollectOnTurnExhaustedRowCarriesTheSameFields(t *testing.T) {
	s, r, _ := newService(t)
	if err := r.Create(context.Background(), &entity.AgentDelegation{
		ID: "a1", RootID: "a1", ParentSessionID: "parent", ProfileKey: "researcher",
		ChildSessionID: "sub-a1", ChildAgent: "claude", TriggeredBy: "user-1",
		Status: entity.DelegationStoppedMaxTurns, TurnsUsed: 9, MaxTurns: 9,
		Result: "got as far as the migration", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed delegation: %v", err)
	}

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !got.Partial {
		t.Fatal("a turn-exhausted row must collect as partial")
	}
	if got.MaxTurns != 9 || got.TurnsUsed != 9 {
		t.Fatalf("turns = %d/%d, want 9/9", got.TurnsUsed, got.MaxTurns)
	}
	if !strings.Contains(got.Note, "a1") {
		t.Fatalf("note does not name the delegation to continue: %q", got.Note)
	}
}
