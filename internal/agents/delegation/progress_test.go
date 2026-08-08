package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// seedRunning creates a delegation mid-flight, which is the only state in
// which a progress peek means anything.
func seedRunning(t *testing.T, r *Repo, id string) *entity.AgentDelegation {
	t.Helper()
	d := &entity.AgentDelegation{
		ID: id, RootID: id, ParentSessionID: "leader", ProfileKey: "researcher",
		ChildSessionID: "sub-" + id, ChildAgent: "researcher", Handle: "researcher",
		Mode: ModeAsync, Status: entity.DelegationRunning, TriggeredBy: "user-1",
	}
	if err := r.Create(context.Background(), d); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return d
}

// The ⭐ guarantee of the PULL half: a supervisor can look at work in
// flight. Without this, a delegation that is still running answers only
// "pending", so the earliest a wrong direction can be caught is after it
// has been fully taken.
func TestCollectShowsWorkInFlight(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "refactoring the storage layer"
	seedRunning(t, r, "a1")

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !got.Pending {
		t.Fatal("a running delegation must still report pending")
	}
	if got.Progress != "refactoring the storage layer" {
		t.Fatalf("progress = %q, want the sub-agent's in-flight work", got.Progress)
	}
	// Progress is NOT the answer. Putting it in Result would make a leader
	// act on a half-finished thought as though the agent had concluded it.
	if got.Result != "" {
		t.Fatalf("result = %q, want empty until the sub-agent finishes", got.Result)
	}
}

// The most expensive bug this feature could introduce. Collect marks a row
// handed-over so a leader never acts on the same answer twice — but if a
// mid-flight peek trips that guard, the sub-agent's REAL result becomes
// unreachable: it finishes, and the leader is told it already saw an
// answer that was never delivered. Work paid for and silently dropped.
func TestPeekingAtProgressDoesNotConsumeTheResult(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "still working"
	seedRunning(t, r, "a1")

	// The supervisor checks in, twice, as a per-minute loop would.
	for i := 0; i < 2; i++ {
		got, err := s.Collect(context.Background(), "a1", "user-1", false)
		if err != nil {
			t.Fatalf("peek %d: %v", i, err)
		}
		if !got.Pending {
			t.Fatalf("peek %d did not report pending", i)
		}
	}

	row, err := r.Get(context.Background(), "a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Collected {
		t.Fatal("peeking marked the row collected — its real result can now never be handed over")
	}

	// The sub-agent finishes, and the answer must still be collectable.
	if _, err := r.FinishGuarded(context.Background(), "a1",
		entity.DelegationRunning, entity.DelegationDone, "the real answer", "", 3); err != nil {
		t.Fatalf("finish: %v", err)
	}
	final, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("final collect: %v", err)
	}
	if final.Result != "the real answer" {
		t.Fatalf("result = %q, want the finished answer", final.Result)
	}
	if strings.Contains(final.Note, "Already collected") {
		t.Fatalf("the answer was consumed by a peek: %q", final.Note)
	}
}

// A terminal row's work is in Result. Reading the runner for one would
// return whatever is left in a buffer for a process that has already
// gone — stale text presented as current activity.
func TestCollectDoesNotReportProgressForAFinishedAgent(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "stale buffer contents"
	d := seedRunning(t, r, "a1")
	if _, err := r.FinishGuarded(context.Background(), d.ID,
		entity.DelegationRunning, entity.DelegationDone, "the answer", "", 1); err != nil {
		t.Fatalf("finish: %v", err)
	}

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Progress != "" {
		t.Fatalf("progress = %q on a finished delegation — that is a dead process's buffer", got.Progress)
	}
	if got.Result != "the answer" {
		t.Fatalf("result = %q", got.Result)
	}
}

// The note a supervisor reads must not tell it to stop supervising. The
// old wording ("do not block on this") was right for a caller waiting on
// the answer and wrong for one deliberately checking in.
func TestPendingNoteInvitesSupervisionButNotWaiting(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "half a thought"
	seedRunning(t, r, "a1")

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	note := strings.ToLower(got.Note)
	if !strings.Contains(note, "message") {
		t.Fatalf("note never says how to correct a wrong direction: %q", got.Note)
	}
	if !strings.Contains(note, "loop") {
		t.Fatalf("note never warns against polling for the answer: %q", got.Note)
	}
}

// CollectPending is the "what is waiting for me" listing. A supervisor
// that used it instead of a by-id collect would otherwise see running
// work as a bare status with no way to judge it.
func TestCollectPendingCarriesProgressToo(t *testing.T) {
	s, r, runner := newService(t)
	runner.partial = "reading the migration files"
	seedRunning(t, r, "a1")

	items, err := s.CollectPending(context.Background(), "leader")
	if err != nil {
		t.Fatalf("collect pending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Progress != "reading the migration files" {
		t.Fatalf("progress = %q", items[0].Progress)
	}
	if items[0].Note == "" {
		t.Fatal("a pending item with no note leaves the leader guessing what to do")
	}
}

/* ── PUSH: the sub-agent reports on its own ─────────────────────────── */

// The ⭐ guarantee of the PUSH half: a report reaches the supervisor
// without being asked for. A note filed silently would add nothing over
// the raw partial text Collect already exposes — the value is that it
// arrives.
func TestProgressWakesTheSupervisor(t *testing.T) {
	s, r, _ := newService(t)
	del := &recordingDeliverer{}
	s.Deliver = del
	seedRunning(t, r, "a1")

	res, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{
		Note: "auth handler works, writing tests now",
	})
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if !res.Recorded || !res.Delivered {
		t.Fatalf("recorded=%v delivered=%v, want both true", res.Recorded, res.Delivered)
	}

	got := del.sessionDeliveries()
	if len(got) != 1 {
		t.Fatalf("%d deliveries, want 1", len(got))
	}
	if !strings.Contains(got[0], "auth handler works") {
		t.Fatalf("the note never reached the leader: %q", got[0])
	}
	// The frame is what stops a leader acting on half a job: a bare note
	// arriving as a user turn reads exactly like a final answer.
	if !strings.Contains(got[0], "NOT a final result") {
		t.Fatalf("delivery does not mark itself as progress: %q", got[0])
	}
	if !strings.Contains(got[0], "Do not collect") {
		t.Fatalf("delivery does not warn against collecting an unfinished run: %q", got[0])
	}
}

// The note is stored so a supervisor that was not woken — or that checks
// later — still sees where the agent got to.
func TestProgressIsRecordedOnTheRow(t *testing.T) {
	s, r, _ := newService(t)
	seedRunning(t, r, "a1")

	if _, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{
		Note: "schema done", Next: "wire the handler", Blocked: "no staging credentials",
	}); err != nil {
		t.Fatalf("progress: %v", err)
	}

	row, err := r.Get(context.Background(), "a1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, want := range []string{"schema done", "wire the handler", "no staging credentials"} {
		if !strings.Contains(row.LastReport, want) {
			t.Fatalf("stored report lost %q: %q", want, row.LastReport)
		}
	}
	if row.LastReportAt == nil {
		t.Fatal("a report with no timestamp cannot be judged stale")
	}

	// And a supervisor reading through collect sees it, which is what
	// makes the note useful to somebody who was never woken.
	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !strings.Contains(got.LastReport, "schema done") {
		t.Fatalf("collect does not surface the report: %q", got.LastReport)
	}
}

// Only the latest position is kept. A growing log would be re-read in
// full on every supervision check, and the earlier positions are already
// in the sub-agent's own transcript.
func TestProgressKeepsOnlyTheLatestPosition(t *testing.T) {
	s, r, _ := newService(t)
	seedRunning(t, r, "a1")

	for _, note := range []string{"reading the spec", "writing the handler", "running the tests"} {
		if _, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{Note: note}); err != nil {
			t.Fatalf("progress %q: %v", note, err)
		}
	}

	row, _ := r.Get(context.Background(), "a1")
	if !strings.Contains(row.LastReport, "running the tests") {
		t.Fatalf("last report = %q, want the most recent", row.LastReport)
	}
	if strings.Contains(row.LastReport, "reading the spec") {
		t.Fatalf("reports accumulated instead of replacing: %q", row.LastReport)
	}
}

// A leader has no delegation row to report against. Refused plainly
// rather than silently accepted: a model that believes it reported
// something it did not will keep working on a false assumption.
func TestProgressRefusedForALeader(t *testing.T) {
	s, r, _ := newService(t)
	seedRunning(t, r, "a1")

	_, err := s.ReportProgress(context.Background(), "leader", ProgressReport{Note: "going well"})
	if !errors.Is(err, ErrNotASubAgent) {
		t.Fatalf("err = %v, want ErrNotASubAgent", err)
	}
}

// A note filed after the result was delivered reaches a leader that has
// already moved on, so it is refused rather than stored.
func TestProgressRefusedAfterTheRunFinished(t *testing.T) {
	s, r, _ := newService(t)
	d := seedRunning(t, r, "a1")
	if _, err := r.FinishGuarded(context.Background(), d.ID,
		entity.DelegationRunning, entity.DelegationDone, "the answer", "", 1); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if _, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{Note: "one more thing"}); err == nil {
		t.Fatal("progress on a finished delegation must be refused")
	}
}

// A wake that did not land costs promptness, not the report. Returning an
// error would tell the sub-agent its report failed and invite it to spend
// another turn re-filing something already on the record.
func TestProgressSurvivesAFailedWake(t *testing.T) {
	s, r, _ := newService(t)
	s.Deliver = failingDeliverer{}
	seedRunning(t, r, "a1")

	res, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{Note: "halfway"})
	if err != nil {
		t.Fatalf("a failed wake must not fail the op: %v", err)
	}
	if !res.Recorded || res.Delivered {
		t.Fatalf("recorded=%v delivered=%v, want recorded but not delivered", res.Recorded, res.Delivered)
	}
	if !strings.Contains(res.Note, "do not re-file") {
		t.Fatalf("ack does not stop a retry: %q", res.Note)
	}

	row, _ := r.Get(context.Background(), "a1")
	if !strings.Contains(row.LastReport, "halfway") {
		t.Fatal("the note was lost when the wake failed")
	}
}

// The acknowledgement must not read as a turn boundary worth waiting at.
// Nothing is coming back — the leader reads the note on its own time.
func TestProgressAckTellsTheAgentToKeepWorking(t *testing.T) {
	s, r, _ := newService(t)
	s.Deliver = &recordingDeliverer{}
	seedRunning(t, r, "a1")

	res, err := s.ReportProgress(context.Background(), "sub-a1", ProgressReport{Note: "halfway"})
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if !strings.Contains(res.Note, "keep working") {
		t.Fatalf("ack does not tell the agent to carry on: %q", res.Note)
	}
	if !strings.Contains(strings.ToLower(res.Note), "do not wait") {
		t.Fatalf("ack does not stop the agent waiting for a reply: %q", res.Note)
	}
}

type failingDeliverer struct{}

func (failingDeliverer) DeliverToChannel(context.Context, string, string) error {
	return errors.New("no channel")
}
func (failingDeliverer) DeliverToSession(context.Context, string, string, string) error {
	return errors.New("leader is gone")
}

// Reporting is asked for, not imposed: a short task must not spend a turn
// filing notes nobody is watching for.
func TestSupervisionBriefOnlyAppearsWhenAsked(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")

	quiet := seedRunning(t, r, "a1")
	if got := s.spawnPreamble(context.Background(), quiet); strings.Contains(got, "being supervised") {
		t.Fatalf("an unsupervised delegation was told to report: %q", got)
	}

	watched := seedRunning(t, r, "a2")
	watched.Supervised = true
	got := s.spawnPreamble(context.Background(), watched)
	if !strings.Contains(got, "being supervised") {
		t.Fatalf("a supervised delegation was never told to report: %q", got)
	}
	// Criteria, not a schedule: a turn is one call, not one unit of
	// progress, so a turn count produces notes at the wrong moments.
	if !strings.Contains(got, "MEANING, not activity") {
		t.Fatalf("the brief does not say what is worth reporting: %q", got)
	}
}

/* ── reaching an agent that has stopped ─────────────────────────────── */

// stopped_max_turns is the state where reaching a sub-agent matters most:
// the work was cut short, not wrong. Refusing messages there is what left
// a leader with no way forward except spawning a stranger.
func TestMessageReachesAStoppedAgent(t *testing.T) {
	for _, status := range []string{
		entity.DelegationDone,
		entity.DelegationStoppedMaxTurns,
		entity.DelegationStoppedBudget,
		entity.DelegationInterrupted,
		entity.DelegationFailed,
	} {
		t.Run(status, func(t *testing.T) {
			s, r, _ := newService(t)
			s.Waker = recordingWaker{}
			s.Steerer = &recordingSteerer{}
			d := seedRunning(t, r, "a1")
			if _, err := r.FinishGuarded(context.Background(), d.ID,
				entity.DelegationRunning, status, "partial work", "", 2); err != nil {
				t.Fatalf("finish: %v", err)
			}

			_, err := s.SendMessage(context.Background(), SendInput{
				RootID: "a1", FromHandle: entity.LeaderHandle, ToHandle: "researcher",
				Body: "keep going", Kind: entity.MessageTell,
			})
			if err != nil {
				t.Fatalf("message to a %s agent was refused: %v", status, err)
			}
		})
	}
}

// A row with no session has nowhere to deliver — that one really is
// unreachable, and saying so beats queueing into a void.
func TestMessageRefusedWithNoSessionToWake(t *testing.T) {
	s, r, _ := newService(t)
	d := &entity.AgentDelegation{
		ID: "a1", RootID: "a1", ParentSessionID: "leader", ProfileKey: "researcher",
		Handle: "researcher", Status: entity.DelegationDone, TriggeredBy: "user-1",
	}
	if err := r.Create(context.Background(), d); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := s.SendMessage(context.Background(), SendInput{
		RootID: "a1", FromHandle: entity.LeaderHandle, ToHandle: "researcher",
		Body: "hello", Kind: entity.MessageTell,
	})
	if err == nil {
		t.Fatal("a delegation with no child session has nowhere to deliver")
	}
}

// "@developer keep going" must mean the developer that did the work. When
// finished handles were dropped, that token fell through to the role
// branch and spawned a stranger with no memory of the thing it was asked
// to follow up on.
func TestFinishedAgentStillResolvesAsAnAgent(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")
	d := seedRunning(t, r, "a1")
	if _, err := r.FinishGuarded(context.Background(), d.ID,
		entity.DelegationRunning, entity.DelegationDone, "done", "", 1); err != nil {
		t.Fatalf("finish: %v", err)
	}

	res, err := s.NewResolver(context.Background(), "a1", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	got := res.Resolve("researcher")
	if got.Kind != TargetAgent {
		t.Fatalf("resolved to %v, want the finished agent — a follow-up must not spawn a stranger", got.Kind)
	}
	status, done := res.Finished("researcher")
	if !done || status != entity.DelegationDone {
		t.Fatalf("finished = (%q, %v), want the terminal status so a caller knows it needs waking", status, done)
	}
}

// Keeping finished handles addressable must not cost the ability to start
// fresh work of the same kind. A review that finished an hour ago should
// not make "review this too" unavailable for the rest of the conversation.
func TestAFinishedRoleCanStillBeSpawnedAgain(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")
	d := seedRunning(t, r, "a1")
	if _, err := r.FinishGuarded(context.Background(), d.ID,
		entity.DelegationRunning, entity.DelegationDone, "done", "", 1); err != nil {
		t.Fatalf("finish: %v", err)
	}

	res, err := s.NewResolver(context.Background(), "a1", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	var found bool
	for _, k := range res.SpawnableRoles() {
		if k == "researcher" {
			found = true
		}
	}
	if !found {
		t.Fatalf("spawnable = %v, want researcher — a finished instance must not retire its role", res.SpawnableRoles())
	}
}

// A LIVE instance still shadows its role: "@researcher" while one is
// working means that one, not a second copy of it.
func TestALiveRoleIsNotSpawnable(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")
	seedRunning(t, r, "a1")

	res, err := s.NewResolver(context.Background(), "a1", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	for _, k := range res.SpawnableRoles() {
		if k == "researcher" {
			t.Fatal("a role with a working instance must not be offered as spawnable")
		}
	}
}

type recordingWaker struct{}

func (recordingWaker) WakeChild(context.Context, string, string) error { return nil }
