package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	wf "github.com/yogasw/wick/internal/agents/workflow"
)

func newTestService(t *testing.T) (*FileService, string) {
	t.Helper()
	dir := t.TempDir()
	layout := config.NewLayout(dir)
	if err := layout.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	return New(layout), dir
}

func makeWorkflow(id string) wf.Workflow {
	return wf.Workflow{
		ID:       id,
		Name:     id,
		Enabled:  true,
		Triggers: []wf.Trigger{{Type: wf.TriggerManual}},
		Graph: wf.Graph{
			Entry: "n1",
			Nodes: []wf.Node{{ID: "n1", Type: wf.NodeShell, Command: []string{"echo", "hi"}}},
		},
	}
}

// TestDraftSaveDoesNotTouchPublished — the whole point of the draft
// lifecycle: save writes to workflow.draft.yaml, never overwrites
// workflow.yaml.
func TestDraftSaveDoesNotTouchPublished(t *testing.T) {
	svc, dir := newTestService(t)
	w := makeWorkflow("wf")
	if err := svc.Create("wf", w, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	publishedPath := filepath.Join(dir, "workflows", "wf", "workflow.yaml")
	pubBefore, _ := os.ReadFile(publishedPath)

	// Mutate + save as draft.
	w2 := w
	w2.Description = "draft edit"
	if err := svc.SaveDraft("wf", w2); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	if !svc.HasDraft("wf") {
		t.Errorf("expected draft to exist after SaveDraft")
	}
	pubAfter, _ := os.ReadFile(publishedPath)
	if string(pubBefore) != string(pubAfter) {
		t.Errorf("SaveDraft must not touch workflow.yaml.\nbefore:\n%s\nafter:\n%s", pubBefore, pubAfter)
	}
	loaded, err := svc.LoadDraft("wf")
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	if loaded.Description != "draft edit" {
		t.Errorf("draft description not persisted: %q", loaded.Description)
	}
}

// TestPublishPromotesDraft — publish copies draft → main and removes
// draft so the router sees the new published version.
func TestPublishPromotesDraft(t *testing.T) {
	svc, dir := newTestService(t)
	w := makeWorkflow("wf")
	if err := svc.Create("wf", w, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	w2 := w
	w2.Description = "ready to publish"
	if err := svc.SaveDraft("wf", w2); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if _, err := svc.Publish("wf"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if svc.HasDraft("wf") {
		t.Errorf("expected draft removed after publish")
	}
	loaded, err := svc.Load("wf")
	if err != nil {
		t.Fatalf("load published: %v", err)
	}
	if loaded.Description != "ready to publish" {
		t.Errorf("publish did not promote draft content, got desc=%q", loaded.Description)
	}
	// Spot-check the draft file is gone on disk.
	draftPath := filepath.Join(dir, "workflows", "wf", "workflow.draft.yaml")
	if _, err := os.Stat(draftPath); !os.IsNotExist(err) {
		t.Errorf("expected draft file removed, stat err=%v", err)
	}
}

// TestServiceToggleSkipsValidation — Service.Toggle just flips the
// enabled flag, it must not run parse.Validate. Earlier the Toggle
// flow on the canvas (Canvas.Toggle → mutate → Validate) refused to
// flip enabled when the workflow had any validation issue, leaving
// users unable to disable a half-built workflow they wanted to stop.
func TestServiceToggleSkipsValidation(t *testing.T) {
	svc, _ := newTestService(t)
	// Build a workflow that would fail validation (classify w/o prompt).
	bad := wf.Workflow{
		ID:       "bad",
		Triggers: []wf.Trigger{{Type: wf.TriggerManual}},
		Graph: wf.Graph{
			Entry: "c",
			Nodes: []wf.Node{{ID: "c", Type: wf.NodeClassify, OutputCases: []string{"a"}}},
		},
	}
	if err := svc.Create("bad", bad, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Toggle("bad", true); err != nil {
		t.Fatalf("toggle on invalid workflow should succeed, got: %v", err)
	}
	loaded, _ := svc.Load("bad")
	if !loaded.Enabled {
		t.Errorf("expected Enabled=true after toggle, got false")
	}
}

// TestDiscardDraftRevertsToPublished — discard wipes the draft and
// next LoadDraft falls back to the published workflow.
func TestDiscardDraftRevertsToPublished(t *testing.T) {
	svc, _ := newTestService(t)
	w := makeWorkflow("wf")
	if err := svc.Create("wf", w, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	w2 := w
	w2.Description = "draft only"
	if err := svc.SaveDraft("wf", w2); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := svc.DiscardDraft("wf"); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if svc.HasDraft("wf") {
		t.Errorf("expected draft gone after Discard")
	}
	loaded, err := svc.LoadDraft("wf")
	if err != nil {
		t.Fatalf("load draft (should fall back): %v", err)
	}
	if loaded.Description == "draft only" {
		t.Errorf("expected revert to published, got the draft desc")
	}
}
