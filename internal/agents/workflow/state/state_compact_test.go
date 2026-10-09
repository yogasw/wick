package state

import (
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func bigRows() []map[string]any {
	rows := make([]map[string]any, 3000)
	for i := range rows {
		rows[i] = map[string]any{"i": i, "pad": strings.Repeat("x", 40)}
	}
	return rows
}

func TestSave_TruncatesBigOutputsOfFinishedSuccessRun(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	end := time.Now()
	in := workflow.RunState{
		RunID: "r1", WorkflowID: "wf", Status: workflow.StatusSuccess, EndedAt: &end,
		Outputs: map[string]any{"scan": map[string]any{"rows": bigRows()}, "small": map[string]any{"ok": true}},
	}
	if err := s.Save("wf", "r1", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("wf", "r1")
	if err != nil {
		t.Fatal(err)
	}
	scan, _ := got.Outputs["scan"].(map[string]any)
	if scan["_truncated"] != true || scan["preview"] == nil {
		t.Fatalf("big output should be a truncated stub, got %v", got.Outputs["scan"])
	}
	if small, _ := got.Outputs["small"].(map[string]any); small["ok"] != true {
		t.Fatal("small output must be stored whole")
	}
	if _, ok := in.Outputs["scan"].(map[string]any)["rows"]; !ok {
		t.Fatal("Save must not mutate the caller's outputs")
	}
}

func TestSave_KeepsFullOutputsWhileRunningOrFailed(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	end := time.Now()
	for _, st := range []workflow.RunState{
		{RunID: "run", WorkflowID: "wf", Status: workflow.StatusRunning},
		{RunID: "bad", WorkflowID: "wf", Status: workflow.StatusFailed, EndedAt: &end},
	} {
		st.Outputs = map[string]any{"scan": map[string]any{"rows": bigRows()}}
		if err := s.Save("wf", st.RunID, st); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Load("wf", st.RunID)
		if m, _ := got.Outputs["scan"].(map[string]any); m["_truncated"] == true {
			t.Fatalf("%s run must keep its full outputs", st.Status)
		}
	}
}
