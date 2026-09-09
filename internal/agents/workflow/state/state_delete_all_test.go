package state

import (
	"fmt"
	"os"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func seedRun(t *testing.T, s *FileStore, id, runID string) {
	t.Helper()
	if err := s.Save(id, runID, workflow.RunState{RunID: runID, WorkflowID: id, Status: "success"}); err != nil {
		t.Fatalf("save %s: %v", runID, err)
	}
	if err := s.AppendEvent(id, runID, workflow.RunEvent{Event: "workflow_started"}); err != nil {
		t.Fatalf("append event %s: %v", runID, err)
	}
	if err := s.IndexAppend(id, IndexEntry{ID: runID, Status: "success"}); err != nil {
		t.Fatalf("index append %s: %v", runID, err)
	}
}

func TestFileStore_DeleteAll_ClearsHistory(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id := "wf1"

	var runIDs []string
	for i := 0; i < 5; i++ {
		runID := fmt.Sprintf("run-%d", i)
		runIDs = append(runIDs, runID)
		seedRun(t, s, id, runID)
	}

	deleted, err := s.DeleteAll(id)
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}
	for _, runID := range runIDs {
		if _, err := os.Stat(layout.WorkflowRunDir(id, runID)); !os.IsNotExist(err) {
			t.Errorf("%s folder still there, stat err = %v", runID, err)
		}
	}
	if entries, _, _ := s.IndexList(id, 1, 100); len(entries) != 0 {
		t.Errorf("index still lists %d rows after clearing", len(entries))
	}
	if runs, _ := s.ListRuns(id); len(runs) > 1 {
		// At most the index dir may remain.
		t.Errorf("ListRuns still returns %v", runs)
	}
}

// The sharded index lives INSIDE runs/, so a naive RemoveAll of the tree
// would take it out too. The store must keep that directory usable: a run
// that lands right after a clear has to be indexed and listed normally.
func TestFileStore_DeleteAll_IndexStillUsable(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id := "wf1"
	seedRun(t, s, id, "run-old")

	if _, err := s.DeleteAll(id); err != nil {
		t.Fatalf("delete all: %v", err)
	}

	seedRun(t, s, id, "run-new")
	entries, _, err := s.IndexList(id, 1, 100)
	if err != nil {
		t.Fatalf("index list after clear: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "run-new" {
		t.Fatalf("want only run-new indexed, got %+v", entries)
	}
	if _, err := s.Load(id, "run-new"); err != nil {
		t.Errorf("run recorded after a clear should load: %v", err)
	}
}

// Clearing one workflow must not touch another's history.
func TestFileStore_DeleteAll_ScopedToOneWorkflow(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	seedRun(t, s, "wf1", "run-a")
	seedRun(t, s, "wf2", "run-b")

	if _, err := s.DeleteAll("wf1"); err != nil {
		t.Fatalf("delete all: %v", err)
	}

	if _, err := s.Load("wf2", "run-b"); err != nil {
		t.Errorf("wf2 run should survive wf1 clear: %v", err)
	}
	if entries, _, _ := s.IndexList("wf2", 1, 100); len(entries) != 1 {
		t.Errorf("wf2 index should be untouched, got %d rows", len(entries))
	}
}

// An empty (or never-run) workflow is not an error — the menu item stays
// clickable and should just report zero.
func TestFileStore_DeleteAll_EmptyIsNoop(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)

	deleted, err := s.DeleteAll("never-ran")
	if err != nil {
		t.Fatalf("delete all on empty workflow: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}
