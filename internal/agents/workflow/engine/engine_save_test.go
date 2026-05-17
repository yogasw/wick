package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// errStore is a state.Store stub whose Save always fails.
type errStore struct{ saveErr error }

func (s *errStore) Save(slug, runID string, st workflow.RunState) error { return s.saveErr }
func (s *errStore) Load(slug, runID string) (workflow.RunState, error)  { return workflow.RunState{}, nil }
func (s *errStore) AppendEvent(slug, runID string, ev workflow.RunEvent) error { return nil }
func (s *errStore) ListEvents(slug, runID string) ([]workflow.RunEvent, error) { return nil, nil }
func (s *errStore) ListRuns(slug string) ([]string, error)                     { return nil, nil }
func (s *errStore) IndexAppend(slug string, entry state.IndexEntry) error      { return nil }
func (s *errStore) IndexList(slug string, page, pageSize int) ([]state.IndexEntry, bool, error) {
	return nil, false, nil
}

// okStore is a state.Store stub that counts Save calls.
type okStore struct{ saved int }

func (s *okStore) Save(slug, runID string, st workflow.RunState) error {
	s.saved++
	return nil
}
func (s *okStore) Load(slug, runID string) (workflow.RunState, error)          { return workflow.RunState{}, nil }
func (s *okStore) AppendEvent(slug, runID string, ev workflow.RunEvent) error  { return nil }
func (s *okStore) ListEvents(slug, runID string) ([]workflow.RunEvent, error)  { return nil, nil }
func (s *okStore) ListRuns(slug string) ([]string, error)                      { return nil, nil }
func (s *okStore) IndexAppend(slug string, entry state.IndexEntry) error       { return nil }
func (s *okStore) IndexList(slug string, page, pageSize int) ([]state.IndexEntry, bool, error) {
	return nil, false, nil
}

func newTestEngine(ss state.Store) *Engine {
	return &Engine{
		StateStore: ss,
		Executors:  map[workflow.NodeType]workflow.Executor{},
		Now:        func() time.Time { return time.Now().UTC() },
		IDGen:      NewRunID,
	}
}

// TestSaveState_LogsOnError verifies a Save failure does not panic —
// the engine continues running after a state write error.
func TestSaveState_LogsOnError(t *testing.T) {
	store := &errStore{saveErr: errors.New("disk full")}
	e := newTestEngine(store)
	st := &workflow.RunState{RunID: "run-1"}

	// Must not panic or return an error — just emit a warning log.
	e.saveState(context.Background(), "my-workflow", st)
}

// TestSaveState_CallsSave verifies saveState actually reaches the store
// (regression guard: the original code used _ = which silently dropped errors).
func TestSaveState_CallsSave(t *testing.T) {
	store := &okStore{}
	e := newTestEngine(store)
	st := &workflow.RunState{RunID: "run-1"}

	e.saveState(context.Background(), "my-workflow", st)

	if store.saved != 1 {
		t.Errorf("expected 1 Save call, got %d", store.saved)
	}
}

// TestSaveState_CalledOnRecordSuccess verifies recordSuccess persists state.
func TestSaveState_CalledOnRecordSuccess(t *testing.T) {
	store := &okStore{}
	e := newTestEngine(store)
	st := &workflow.RunState{RunID: "run-2", Current: []string{}, Outputs: map[string]any{}}
	rc := &workflow.RunContext{
		Outputs:     map[string]any{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	n := workflow.Node{ID: "n1", Type: workflow.NodeShell}

	e.recordSuccess(context.Background(), "wf", st, rc, n, workflow.NodeOutput{}, 0)

	if store.saved == 0 {
		t.Error("recordSuccess should have called saveState")
	}
}

// TestSaveState_CalledOnFailNode verifies failNode persists state.
func TestSaveState_CalledOnFailNode(t *testing.T) {
	store := &okStore{}
	e := newTestEngine(store)
	st := &workflow.RunState{RunID: "run-3"}
	n := workflow.Node{ID: "n1", Type: workflow.NodeShell}

	_ = e.failNode(context.Background(), "wf", st, n, errors.New("boom"))

	if store.saved == 0 {
		t.Error("failNode should have called saveState")
	}
}

// TestSaveState_MultipleCallsAllReachStore verifies each saveState call
// reaches the store independently.
func TestSaveState_MultipleCallsAllReachStore(t *testing.T) {
	store := &okStore{}
	e := newTestEngine(store)
	st := &workflow.RunState{RunID: "run-4"}

	for i := 0; i < 5; i++ {
		e.saveState(context.Background(), "wf", st)
	}

	if store.saved != 5 {
		t.Errorf("expected 5 Save calls, got %d", store.saved)
	}
}
