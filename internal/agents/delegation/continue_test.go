package delegation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// specRecorder captures every ChildSpec a runner is asked to start, which
// is how these tests see WHICH session a continuation actually landed in.
type specRecorder struct {
	mu    sync.Mutex
	specs []ChildSpec
}

func (r *specRecorder) record(spec ChildSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, spec)
}

func (r *specRecorder) all() []ChildSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ChildSpec, len(r.specs))
	copy(out, r.specs)
	return out
}

// runAndContinue drives one delegation to a terminal status, then
// continues it, returning both results and every spec the runner saw.
func runAndContinue(
	t *testing.T,
	first Request,
	cont ContinueRequest,
	stream EventStream,
) (*Result, *Result, []ChildSpec, *Repo) {
	t.Helper()
	rec := &specRecorder{}
	runner := &fakeRunner{partial: "half an answer"}
	runner.onStart = rec.record
	s, r, _ := runService(t, stream, runner)

	res1, err := s.Run(context.Background(), first)
	if err != nil {
		t.Fatalf("first leg: %v", err)
	}
	cont.DelegationID = res1.DelegationID
	if cont.ActorID == "" {
		cont.ActorID = first.TriggeredBy
	}
	res2, err := s.Continue(context.Background(), cont)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	return res1, res2, rec.all(), r
}

// The ⭐ guarantee of this whole feature: continuing does not mint a new
// session. Everything else here is in service of this one property.
func TestContinueReusesTheSameChildSession(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "first answer"},
		{Type: event.Done},
	}}
	res1, res2, specs, r := runAndContinue(t,
		baseReq(),
		ContinueRequest{Task: "now check the tests too"},
		stream,
	)

	if res1.DelegationID != res2.DelegationID {
		t.Fatalf("continue minted a new delegation: %q → %q", res1.DelegationID, res2.DelegationID)
	}
	if len(specs) != 2 {
		t.Fatalf("runner started %d agents, want 2 (one per leg)", len(specs))
	}
	if specs[0].SessionID != specs[1].SessionID {
		t.Fatalf("continuation ran in a DIFFERENT session: %q vs %q — the transcript is lost",
			specs[0].SessionID, specs[1].SessionID)
	}
	if specs[0].AgentName != specs[1].AgentName {
		t.Fatalf("continuation ran as a different agent: %q vs %q", specs[0].AgentName, specs[1].AgentName)
	}
	if !res2.Continued {
		t.Fatal("a continued result must say so, or the leader reads it as fresh work")
	}

	// One row, not two: the tree must not grow a sibling per continuation.
	rows, err := r.ListByRoot(context.Background(), res1.DelegationID)
	if err != nil {
		t.Fatalf("list by root: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("root has %d delegations after a continue, want 1", len(rows))
	}
}

// A turn-exhausted sub-agent is the main reason to continue, so it must
// not stop again on its very first turn. The cap is absolute and the
// counter is seeded from the row, so the budget has to be RAISED rather
// than re-set to the same number it already hit.
func TestContinueRaisesTheTurnCapAboveWhatWasSpent(t *testing.T) {
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done}, {Type: event.Done},
	}}
	first := baseReq()
	first.MaxTurns = 2

	res1, res2, _, r := runAndContinue(t,
		first,
		ContinueRequest{Task: "keep going", ExtraTurns: 2},
		stream,
	)

	if res1.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("first leg status = %q, want %q", res1.Status, entity.DelegationStoppedMaxTurns)
	}
	if res1.TurnsUsed != 2 {
		t.Fatalf("first leg turns = %d, want 2", res1.TurnsUsed)
	}
	// Four turns total: the two it had already spent plus the two granted.
	if res2.TurnsUsed != 4 {
		t.Fatalf("after continue turns = %d, want 4 — the counter must carry over, not restart", res2.TurnsUsed)
	}

	got, err := r.Get(context.Background(), res1.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MaxTurns != 4 {
		t.Fatalf("max_turns = %d, want 4 (2 spent + 2 granted)", got.MaxTurns)
	}
}

// Without an explicit number, "continue" means another full allowance.
func TestContinueWithoutAnExplicitBudgetGrantsAnotherAllowance(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"},
		{Type: event.Done},
	}}
	first := baseReq()
	first.MaxTurns = 3

	res1, _, _, r := runAndContinue(t,
		first,
		ContinueRequest{Task: "carry on"},
		stream,
	)

	got, err := r.Get(context.Background(), res1.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MaxTurns <= res1.TurnsUsed {
		t.Fatalf("max_turns = %d with %d already spent — the sub-agent has no room to work",
			got.MaxTurns, res1.TurnsUsed)
	}
}

// The previous leg's ending must be cleared, or the continuation's own
// result is unreachable: collect refuses a row it already handed over.
func TestContinueClearsTheCollectedFlag(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "first answer"},
		{Type: event.Done},
	}}
	rec := &specRecorder{}
	runner := &fakeRunner{}
	runner.onStart = rec.record
	s, r, _ := runService(t, stream, runner)

	res1, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("first leg: %v", err)
	}
	// The leader picks up the first answer, as it would in a real flow.
	if _, err := s.Collect(context.Background(), res1.DelegationID, "user-1", false); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if _, err := s.Continue(context.Background(), ContinueRequest{
		DelegationID: res1.DelegationID, Task: "one more thing", ActorID: "user-1",
	}); err != nil {
		t.Fatalf("continue: %v", err)
	}

	got, err := r.Get(context.Background(), res1.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Collected {
		t.Fatal("collected survived the continue — the new result can never be handed over")
	}

	// And the leader can actually pick the second answer up. This is the
	// point of clearing the flag, so assert the outcome rather than only
	// the column: a collect that comes back "already collected" means the
	// continuation's work was paid for and never delivered.
	second, err := s.Collect(context.Background(), res1.DelegationID, "user-1", false)
	if err != nil {
		t.Fatalf("collect after continue: %v", err)
	}
	if strings.Contains(second.Note, "Already collected") {
		t.Fatalf("the continuation's result was unreachable: %q", second.Note)
	}
}

// Continuing a sub-agent that has not stopped would put two drivers on
// one session, interleaving with the turn already in flight.
func TestContinueRefusesARunningDelegation(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")
	row := seedDelegation(t, r, "d-running", "root-1", entity.DelegationRunning, 1)

	_, err := s.Continue(context.Background(), ContinueRequest{
		DelegationID: row.ID, Task: "keep going", ActorID: row.TriggeredBy,
	})
	if !errors.Is(err, ErrNotContinuable) {
		t.Fatalf("err = %v, want ErrNotContinuable", err)
	}
	// The refusal has to name the way out, or a model retries it verbatim.
	if !strings.Contains(err.Error(), "message") {
		t.Fatalf("refusal %q does not tell the caller to use message instead", err)
	}
}

// Continuing someone else's sub-agent is the same authority as stopping
// it: the human who triggered the tree, or an admin.
func TestContinueRefusesAnotherUsersDelegation(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher")
	row := seedDelegation(t, r, "d-done", "root-1", entity.DelegationDone, 1)

	_, err := s.Continue(context.Background(), ContinueRequest{
		DelegationID: row.ID, Task: "keep going", ActorID: "someone-else",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// A sub-agent whose transcript cannot be resumed wakes in the same
// session with no memory of its own work. Saying so is the difference
// between a leader writing a usable follow-up and one writing a
// reference to work the agent can no longer see.
func TestContinueReportsWhenTheTranscriptCouldNotBeResumed(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "first answer"},
		{Type: event.Done},
	}}
	rec := &specRecorder{}
	runner := &fakeRunner{}
	runner.onStart = rec.record
	s, _, _ := runService(t, stream, runner)
	s.Resumable = func(string, string) bool { return false }

	res1, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("first leg: %v", err)
	}
	res2, err := s.Continue(context.Background(), ContinueRequest{
		DelegationID: res1.DelegationID, Task: "carry on", ActorID: "user-1",
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}

	if res2.Resumed {
		t.Fatal("resumed = true when the prober said the transcript was gone")
	}
	if !strings.Contains(res2.Note, "does not remember") {
		t.Fatalf("note = %q, want it to spell out the memory loss", res2.Note)
	}

	// With nothing to refer back to, the task must NOT be framed as a
	// continuation — that would point the agent at work it cannot see.
	specs := rec.all()
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if strings.Contains(specs[1].Task, "already finished one round") {
		t.Fatalf("a non-resumable continuation was framed as one: %q", specs[1].Task)
	}
}

// A resumable continuation is framed so the sub-agent knows it is picking
// up its own work rather than answering a new question. Models resumed
// mid-transcript otherwise re-answer the original brief.
func TestContinueFramesTheTaskByWhyItStopped(t *testing.T) {
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done},
	}}
	first := baseReq()
	first.MaxTurns = 1

	_, _, specs, _ := runAndContinue(t,
		first,
		ContinueRequest{Task: "finish the audit", ExtraTurns: 1},
		stream,
	)

	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	task := specs[1].Task
	if !strings.Contains(task, "ran out of turns") {
		t.Fatalf("continuation task does not say why it stopped: %q", task)
	}
	if !strings.Contains(task, "finish the audit") {
		t.Fatalf("continuation task lost the actual instruction: %q", task)
	}
}

// The caller's original `context` argument belongs to the first leg. A
// sub-agent that already read it does not need it again, and replaying it
// competes with the transcript it is meant to build on.
func TestContinueDoesNotReplayTheOriginalContext(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "first answer"},
		{Type: event.Done},
	}}
	first := baseReq()
	first.Context = "the changelog lives in docs/CHANGELOG.md"

	_, _, specs, _ := runAndContinue(t,
		first,
		ContinueRequest{Task: "now summarise it"},
		stream,
	)

	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if !strings.Contains(specs[0].Task, "docs/CHANGELOG.md") {
		t.Fatalf("first leg lost its context: %q", specs[0].Task)
	}
	if strings.Contains(specs[1].Task, "docs/CHANGELOG.md") {
		t.Fatalf("continuation replayed the first leg's context: %q", specs[1].Task)
	}
}
