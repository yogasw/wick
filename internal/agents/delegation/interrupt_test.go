package delegation

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// fakeRunner records kills so tests can assert that a queued delegation
// is cancelled WITHOUT touching the process manager.
type fakeRunner struct {
	kills   []string
	partial string
	killErr error
}

func (f *fakeRunner) EnsureChildSession(context.Context, string, string, string, string) error {
	return nil
}
func (f *fakeRunner) StartAgent(context.Context, ChildSpec) error { return nil }
func (f *fakeRunner) KillAgent(sessionID, agentName string) error {
	f.kills = append(f.kills, sessionID+"::"+agentName)
	return f.killErr
}
func (f *fakeRunner) PartialText(string, string) string { return f.partial }

func newService(t *testing.T) (*Service, *Repo, *fakeRunner) {
	t.Helper()
	r := testRepo(t)
	run := &fakeRunner{partial: "half an answer"}
	return &Service{Repo: r, Runner: run, Limits: DefaultLimits()}, r, run
}

func TestInterruptRunningKillsAndKeepsPartial(t *testing.T) {
	s, r, run := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 3)

	out, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeKilled {
		t.Fatalf("outcome = %q, want %q", out, OutcomeKilled)
	}
	if len(run.kills) != 1 {
		t.Fatalf("kills = %v, want exactly one", run.kills)
	}

	got, err := r.Get(context.Background(), "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != entity.DelegationInterrupted {
		t.Fatalf("status = %q, want %q", got.Status, entity.DelegationInterrupted)
	}
	// Partial work must survive the stop: it is what the leader gets
	// back instead of an error.
	if got.Result != "half an answer" {
		t.Fatalf("result = %q, want the captured partial", got.Result)
	}
}

// The bug this guards: a queued child has no process, so calling kill
// finds nothing, and a naive implementation reports failure and leaves
// the row queued — the Stop button silently does nothing and the work
// runs anyway.
func TestInterruptQueuedDequeuesWithoutKilling(t *testing.T) {
	s, r, run := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationQueued, 0)

	out, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeDequeued {
		t.Fatalf("outcome = %q, want %q", out, OutcomeDequeued)
	}
	if len(run.kills) != 0 {
		t.Fatalf("kills = %v, want none for a queued delegation", run.kills)
	}

	got, _ := r.Get(context.Background(), "d1")
	if got.Status != entity.DelegationInterrupted {
		t.Fatalf("status = %q, want %q", got.Status, entity.DelegationInterrupted)
	}
}

// The race that matters most: a child finishes successfully at the exact
// moment a human clicks Stop. The successful result must stand.
func TestInterruptDoesNotOverwriteCompletedResult(t *testing.T) {
	s, r, _ := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 4)
	if _, err := r.FinishGuarded(context.Background(), "d1", entity.DelegationDone, entity.DelegationDone, "the real answer", "", 4); err != nil {
		t.Fatalf("seed result: %v", err)
	}

	out, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeAlreadyDone {
		t.Fatalf("outcome = %q, want %q", out, OutcomeAlreadyDone)
	}

	got, _ := r.Get(context.Background(), "d1")
	if got.Status != entity.DelegationDone || got.Result != "the real answer" {
		t.Fatalf("completed result was clobbered: status=%q result=%q", got.Status, got.Result)
	}
}

func TestInterruptTwiceIsIdempotent(t *testing.T) {
	s, r, _ := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 2)

	first, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil || first != OutcomeKilled {
		t.Fatalf("first interrupt: out=%q err=%v", first, err)
	}
	second, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("second interrupt: %v", err)
	}
	if second != OutcomeAlreadyDone {
		t.Fatalf("second outcome = %q, want %q", second, OutcomeAlreadyDone)
	}

	got, _ := r.Get(context.Background(), "d1")
	if got.Result != "half an answer" {
		t.Fatalf("result mutated by the second interrupt: %q", got.Result)
	}
}

// The storage guard must hold even when two writers race directly,
// bypassing Interrupt's pre-check.
func TestFinishGuardedRejectsWrongExpectedStatus(t *testing.T) {
	r := testRepo(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 0)

	ok, err := r.FinishGuarded(context.Background(), "d1", entity.DelegationRunning, entity.DelegationDone, "done", "", 3)
	if err != nil || !ok {
		t.Fatalf("first write should win: ok=%v err=%v", ok, err)
	}
	ok, err = r.FinishGuarded(context.Background(), "d1", entity.DelegationRunning, entity.DelegationInterrupted, "partial", "", 3)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if ok {
		t.Fatal("second write won; the status guard is not holding")
	}
}

func TestInterruptForbiddenForOtherUsers(t *testing.T) {
	s, r, run := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 1)

	if _, err := s.Interrupt(context.Background(), "d1", "someone-else", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(run.kills) != 0 {
		t.Fatalf("kills = %v, want none for an unauthorized actor", run.kills)
	}
	got, _ := r.Get(context.Background(), "d1")
	if got.Status != entity.DelegationRunning {
		t.Fatalf("status = %q, want still running", got.Status)
	}
}

// A child that already exited on its own makes KillAgent report "not
// active". That is not a failure — the status still has to be written,
// or the row stays running forever.
func TestInterruptRecordsStatusWhenAgentAlreadyGone(t *testing.T) {
	s, r, run := newService(t)
	run.killErr = errors.New("agent not active in pool")
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 2)

	out, err := s.Interrupt(context.Background(), "d1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeKilled {
		t.Fatalf("outcome = %q, want %q", out, OutcomeKilled)
	}
	got, _ := r.Get(context.Background(), "d1")
	if got.Status != entity.DelegationInterrupted {
		t.Fatalf("status = %q, want %q", got.Status, entity.DelegationInterrupted)
	}
}

func TestInterruptAllStopsWholeSubtreeDeepestFirst(t *testing.T) {
	s, r, _ := newService(t)

	// leader → d1 (child sub-d1) → d2 (grandchild)
	d1 := &entity.AgentDelegation{
		ID: "d1", RootID: "d1", ParentSessionID: "leader", ProfileKey: "coder",
		ChildSessionID: "sub-d1", ChildAgent: "claude",
		Status: entity.DelegationRunning, TriggeredBy: "user-1",
	}
	d2 := &entity.AgentDelegation{
		ID: "d2", RootID: "d1", ParentSessionID: "sub-d1", ProfileKey: "researcher",
		ChildSessionID: "sub-d2", ChildAgent: "claude", Depth: 1,
		Status: entity.DelegationQueued, TriggeredBy: "user-1",
	}
	for _, d := range []*entity.AgentDelegation{d1, d2} {
		if err := r.Create(context.Background(), d); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	n, err := s.InterruptAll(context.Background(), "leader", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt all: %v", err)
	}
	if n != 2 {
		t.Fatalf("stopped %d, want 2 (child and grandchild)", n)
	}
	for _, id := range []string{"d1", "d2"} {
		got, _ := r.Get(context.Background(), id)
		if !entity.IsTerminalDelegationStatus(got.Status) {
			t.Fatalf("%s left in %q; nothing may survive Stop all", id, got.Status)
		}
	}
}

func TestListActiveDescendantsIsDeepestFirst(t *testing.T) {
	r := testRepo(t)
	rows := []*entity.AgentDelegation{
		{ID: "d1", RootID: "d1", ParentSessionID: "leader", ChildSessionID: "sub-d1", Status: entity.DelegationRunning, Depth: 0},
		{ID: "d2", RootID: "d1", ParentSessionID: "sub-d1", ChildSessionID: "sub-d2", Status: entity.DelegationRunning, Depth: 1},
	}
	for _, d := range rows {
		if err := r.Create(context.Background(), d); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := r.ListActiveDescendants(context.Background(), "leader")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	// Grandchild before child: a parent must not be stopped while its own
	// children are still live.
	if got[0].ID != "d2" {
		t.Fatalf("order = [%s %s], want deepest (d2) first", got[0].ID, got[1].ID)
	}
}
