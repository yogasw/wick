package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// watchStore is a stub state.Store carrying pre-canned index rows +
// per-run state lookups. Only methods Watch hits are populated; the
// rest return defaults so accidental misuse stays loud.
//
// mu guards the two maps so the long-poll tests (TestWatchLongPoll*)
// can fire addRun from a goroutine while the main test goroutine is
// inside Watch → watchPeek/IndexList. Without it -race trips on map
// reads/writes the moment the goroutine wins a race against peek.
type watchStore struct {
	mu     sync.Mutex
	index  map[string][]state.IndexEntry  // workflow-id → rows (newest first)
	states map[[2]string]workflow.RunState // (wfID, runID) → state
}

func newWatchStore() *watchStore {
	return &watchStore{
		index:  map[string][]state.IndexEntry{},
		states: map[[2]string]workflow.RunState{},
	}
}

func (s *watchStore) addRun(wfID, runID, status string, triggerID string, started time.Time, nodes []string) {
	end := started.Add(time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index[wfID] = append(s.index[wfID], state.IndexEntry{
		ID: runID, Status: status, StartedAt: started, EndedAt: &end,
	})
	s.states[[2]string{wfID, runID}] = workflow.RunState{
		RunID:      runID,
		WorkflowID: wfID,
		Status:     status,
		StartedAt:  started,
		EndedAt:    &end,
		Completed:  nodes,
		Event:      workflow.Event{Type: "channel", Channel: "slack", TriggerID: triggerID},
	}
}

func (s *watchStore) Save(string, string, workflow.RunState) error { return nil }
func (s *watchStore) Load(id, runID string) (workflow.RunState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[[2]string{id, runID}]
	if !ok {
		return workflow.RunState{}, nil
	}
	return st, nil
}
func (s *watchStore) AppendEvent(string, string, workflow.RunEvent) error    { return nil }
func (s *watchStore) ListEvents(string, string) ([]workflow.RunEvent, error) { return nil, nil }
func (s *watchStore) ListRuns(string) ([]string, error)                      { return nil, nil }
func (s *watchStore) IndexAppend(string, state.IndexEntry) error             { return nil }
func (s *watchStore) IndexList(id string, page, pageSize int) ([]state.IndexEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.index[id]
	if pageSize <= 0 {
		pageSize = 50
	}
	from := page * pageSize
	if from >= len(rows) {
		return nil, false, nil
	}
	to := from + pageSize
	if to > len(rows) {
		to = len(rows)
	}
	// Return a copy so callers iterate the snapshot without touching
	// the shared backing array — otherwise a follow-up addRun could
	// realloc the slice underneath them.
	out := make([]state.IndexEntry, to-from)
	copy(out, rows[from:to])
	return out, to < len(rows), nil
}

func TestWatchPeekStatus(t *testing.T) {
	s := newWatchStore()
	now := time.Now().UTC()
	s.addRun("wf1", "r1", "success", "trig_a", now.Add(-5*time.Second), []string{"a"})
	s.addRun("wf1", "r2", "failed", "trig_a", now.Add(-3*time.Second), []string{"a"})
	s.addRun("wf1", "r3", "success", "trig_b", now.Add(-1*time.Second), []string{"a"})

	m := &Ops{StateStore: s, Service: nil}
	res, err := m.Watch(context.Background(), WatchInput{
		WorkflowID: "wf1",
		Status:     "failed",
		Since:      "-1m",
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(res.Runs) != 1 || res.Runs[0].RunID != "r2" {
		t.Fatalf("expected [r2], got %+v", res.Runs)
	}
}

func TestWatchPeekTriggerFilter(t *testing.T) {
	s := newWatchStore()
	now := time.Now().UTC()
	s.addRun("wf1", "r1", "success", "trig_a", now.Add(-5*time.Second), []string{"x"})
	s.addRun("wf1", "r2", "success", "trig_b", now.Add(-3*time.Second), []string{"x"})

	m := &Ops{StateStore: s}
	res, err := m.Watch(context.Background(), WatchInput{
		WorkflowID: "wf1",
		TriggerID:  "trig_b",
		Since:      "-1m",
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(res.Runs) != 1 || res.Runs[0].RunID != "r2" {
		t.Fatalf("expected [r2], got %+v", res.Runs)
	}
	if res.Runs[0].TriggerID != "trig_b" {
		t.Fatalf("TriggerID not enriched: %+v", res.Runs[0])
	}
}

func TestWatchPeekNodeFilter(t *testing.T) {
	s := newWatchStore()
	now := time.Now().UTC()
	s.addRun("wf1", "r1", "success", "trig_a", now.Add(-5*time.Second), []string{"alpha"})
	s.addRun("wf1", "r2", "success", "trig_a", now.Add(-3*time.Second), []string{"beta"})

	m := &Ops{StateStore: s}
	res, err := m.Watch(context.Background(), WatchInput{
		WorkflowID: "wf1",
		NodeID:     "beta",
		Since:      "-1m",
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(res.Runs) != 1 || res.Runs[0].RunID != "r2" {
		t.Fatalf("expected [r2], got %+v", res.Runs)
	}
}

func TestWatchPeekLimit(t *testing.T) {
	s := newWatchStore()
	now := time.Now().UTC()
	for i := 0; i < 30; i++ {
		s.addRun("wf1", string(rune('a'+i))+"x", "success", "", now.Add(-time.Duration(i)*time.Second), nil)
	}
	m := &Ops{StateStore: s}
	res, _ := m.Watch(context.Background(), WatchInput{
		WorkflowID: "wf1",
		Limit:      5,
		Since:      "-1m",
	})
	if len(res.Runs) != 5 {
		t.Fatalf("limit not honored, got %d rows", len(res.Runs))
	}
}

func TestWatchSinceDefaults(t *testing.T) {
	// Default since = now. A run started before "now" should NOT match.
	s := newWatchStore()
	s.addRun("wf1", "r1", "success", "", time.Now().Add(-5*time.Second), nil)
	m := &Ops{StateStore: s}
	res, _ := m.Watch(context.Background(), WatchInput{WorkflowID: "wf1"})
	if len(res.Runs) != 0 {
		t.Fatalf("expected empty (since=now), got %+v", res.Runs)
	}
}

func TestWatchExpectStopOnFirst(t *testing.T) {
	// StopOnFirst with peek should still return whatever's available.
	s := newWatchStore()
	now := time.Now().UTC()
	s.addRun("wf1", "r1", "success", "", now.Add(-5*time.Second), nil)
	s.addRun("wf1", "r2", "success", "", now.Add(-3*time.Second), nil)
	m := &Ops{StateStore: s}
	res, _ := m.Watch(context.Background(), WatchInput{
		WorkflowID:  "wf1",
		StopOnFirst: true,
		Since:       "-1m",
	})
	if len(res.Runs) != 1 {
		t.Fatalf("expected 1 row (StopOnFirst), got %+v", res.Runs)
	}
}

func TestWatchLongPollEarlyReturn(t *testing.T) {
	s := newWatchStore()
	now := time.Now().UTC()
	eng := &engine.Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]engine.NodeDescriptor{},
		Triggers:    engine.NewTriggerRegistry(),
	}
	m := &Ops{StateStore: s, Engine: eng}

	// Schedule a run to "land" mid-wait by injecting into the store
	// then publishing a workflow_completed event via the broker.
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.addRun("wf1", "r_live", "success", "trig_a", now, nil)
		// Subscribe-from-engine for fan-out target: we need to use
		// the same Engine instance, so reach in via Engine's public
		// API. Watch is already subscribed by the time this goroutine
		// fires (50ms after the Watch call below).
		// publish directly.
		ev := workflow.RunEvent{TS: now, Event: workflow.EventWorkflowCompleted}
		eng.PublishForTest("wf1", "r_live", ev)
	}()

	start := time.Now()
	res, err := m.Watch(context.Background(), WatchInput{
		WorkflowID:  "wf1",
		WaitSeconds: 5,
		Expect:      1,
		Since:       "-1m",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if len(res.Runs) != 1 || res.Runs[0].RunID != "r_live" {
		t.Fatalf("expected [r_live], got %+v", res.Runs)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected early return well under wait_seconds=5; took %v", elapsed)
	}
}

func TestWatchLongPollTimeout(t *testing.T) {
	s := newWatchStore()
	eng := &engine.Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]engine.NodeDescriptor{},
		Triggers:    engine.NewTriggerRegistry(),
	}
	m := &Ops{StateStore: s, Engine: eng}

	start := time.Now()
	res, _ := m.Watch(context.Background(), WatchInput{
		WorkflowID:  "wf1",
		WaitSeconds: 1,
		Expect:      1,
	})
	elapsed := time.Since(start)
	if len(res.Runs) != 0 {
		t.Fatalf("expected empty on timeout, got %+v", res.Runs)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("expected full wait_seconds elapse, took %v", elapsed)
	}
}
