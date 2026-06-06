package state

import (
	"os"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestFileStore_Delete_RemovesFolderAndIndex(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id, runID := "wf1", "run-abc"

	if err := s.Save(id, runID, workflow.RunState{RunID: runID, WorkflowID: id, Status: "success"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.AppendEvent(id, runID, workflow.RunEvent{Event: "workflow_started"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := s.IndexAppend(id, IndexEntry{ID: runID, Status: "success"}); err != nil {
		t.Fatalf("index append: %v", err)
	}

	runDir := layout.WorkflowRunDir(id, runID)
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("run dir should exist before delete: %v", err)
	}

	if err := s.Delete(id, runID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("run folder must be removed, stat err = %v", err)
	}
	if _, err := s.Load(id, runID); err == nil {
		t.Fatal("Load should fail after delete")
	}
	entries, _, _ := s.IndexList(id, 1, 100)
	for _, e := range entries {
		if e.ID == runID {
			t.Fatal("index still lists the deleted run")
		}
	}
}
