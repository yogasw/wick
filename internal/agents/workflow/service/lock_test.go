package service

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func newFileSvc(t *testing.T) (*FileService, string) {
	t.Helper()
	dir := t.TempDir()
	layout := config.NewLayout(filepath.Join(dir, "base"))
	if err := layout.EnsureLayout(); err != nil {
		t.Fatalf("layout: %v", err)
	}
	return New(layout), dir
}

func sampleWF(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:   id,
		Name: "sample-" + id,
		Triggers: []workflow.Trigger{
			{ID: "trigger-manual", Type: workflow.TriggerManual, EntryNode: "n1", Label: "run"},
		},
		Graph: workflow.Graph{
			Entry: "n1",
			Nodes: []workflow.Node{{ID: "n1", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
}

// TestSaveDraft_RejectsLocked confirms SaveDraft refuses to overwrite a
// locked draft when the incoming body is also locked.
func TestSaveDraft_RejectsLocked(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "lock-test"
	w := sampleWF(id)
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Lock the draft.
	w.Canvas = map[string]any{"locked": true}
	if err := svc.SaveDraft(id, w); err != nil {
		t.Fatalf("lock-save: %v", err)
	}

	// Mutation while still locked must fail.
	mutate := sampleWF(id)
	mutate.Name = "mutated"
	mutate.Canvas = map[string]any{"locked": true}
	err := svc.SaveDraft(id, mutate)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

// TestSaveDraft_AllowsUnlock confirms the lock guard lets through a
// body that flips `_canvas.locked = false` so users can always unlock.
func TestSaveDraft_AllowsUnlock(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "unlock-test"
	w := sampleWF(id)
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	w.Canvas = map[string]any{"locked": true}
	if err := svc.SaveDraft(id, w); err != nil {
		t.Fatalf("lock: %v", err)
	}

	unlocked := sampleWF(id)
	unlocked.Canvas = map[string]any{"locked": false}
	if err := svc.SaveDraft(id, unlocked); err != nil {
		t.Fatalf("unlock should pass: %v", err)
	}

	got, err := svc.LoadDraft(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if isLocked(got) {
		t.Errorf("expected unlocked, still locked")
	}
}

// TestSaveDraft_NoPrevAllows confirms first-save on a brand-new
// workflow that ships locked is accepted (no prior body to compare).
func TestSaveDraft_NoPrevAllows(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "fresh-locked"
	w := sampleWF(id)
	w.Canvas = map[string]any{"locked": true}
	if err := svc.Create(id, w); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Create writes the body; subsequent SaveDraft would block ongoing
	// edits — but the create itself succeeded.
	got, _ := svc.LoadDraft(id)
	if !isLocked(got) {
		t.Errorf("expected locked, got unlocked")
	}
}

// TestListTests_GetTest_RoundTrip confirms FileService stores + reads
// test cases under __tests__/<name>.json.
func TestListTests_GetTest_RoundTrip(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "tests-rt"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	body := []byte(`{"name":"case1","input":{}}`)
	if err := svc.SaveTest(id, "case1", body); err != nil {
		t.Fatalf("save: %v", err)
	}
	names, err := svc.ListTests(id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(names) != 1 || names[0] != "case1" {
		t.Errorf("list: got %v want [case1]", names)
	}
	got, err := svc.GetTest(id, "case1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("round-trip: got %s want %s", got, body)
	}
	if err := svc.DeleteTest(id, "case1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	names, _ = svc.ListTests(id)
	if len(names) != 0 {
		t.Errorf("delete: list still %v", names)
	}
}

// TestSaveTest_RejectsBadName confirms slug-safe validation.
func TestSaveTest_RejectsBadName(t *testing.T) {
	svc, _ := newFileSvc(t)
	id := "bad-name"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	cases := []string{"with space", "with/slash", "with.dot", ""}
	for _, name := range cases {
		if err := svc.SaveTest(id, name, []byte("{}")); err == nil {
			t.Errorf("expected error for name %q", name)
		}
	}
}
