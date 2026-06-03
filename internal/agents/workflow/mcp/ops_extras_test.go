package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/guard"
	"github.com/yogasw/wick/internal/agents/workflow/nodes"
	"github.com/yogasw/wick/internal/agents/workflow/repository"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
	"github.com/yogasw/wick/internal/entity"
)

func newOpsForExtras(t *testing.T) (*Ops, *service.DBService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Workflow{}, &entity.WorkflowVersion{}, &entity.WorkflowTestCase{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dir := t.TempDir()
	layout := config.NewLayout(filepath.Join(dir, "base"))
	if err := layout.EnsureLayout(); err != nil {
		t.Fatalf("layout: %v", err)
	}
	repo := repository.New(db)
	svc := service.NewDB(layout, repo)
	stateStore := state.New(layout)
	eng := engine.New(layout, svc, stateStore)
	// Need at least the end + transform executors so ExecNode tests
	// can find them.
	eng.Register(workflow.NodeEnd, nodes.NewEndExecutor())
	eng.Register(workflow.NodeTransform, nodes.NewTransformExecutor())

	ops := &Ops{
		Service:    svc,
		Engine:     eng,
		StateStore: stateStore,
		Repo:       repo,
		Guard:      guard.New(guard.Config{}),
	}
	return ops, svc
}

func sampleWF(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:   id,
		Name: "wf-" + id,
		Triggers: []workflow.Trigger{
			{ID: "trigger-manual", Type: workflow.TriggerManual, EntryNode: "n1", Label: "run"},
		},
		Graph: workflow.Graph{
			Entry: "n1",
			Nodes: []workflow.Node{{ID: "n1", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
}

// TestOps_SetLock round-trips the locked flag through workflow.Canvas.
func TestOps_SetLock(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "lock"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ops.SetLock(id, true); err != nil {
		t.Fatalf("lock: %v", err)
	}
	w, _ := svc.LoadDraft(id)
	if locked, _ := w.Canvas["locked"].(bool); !locked {
		t.Errorf("expected locked=true, got %v", w.Canvas)
	}
	if err := ops.SetLock(id, false); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	w, _ = svc.LoadDraft(id)
	if _, present := w.Canvas["locked"]; present {
		t.Errorf("expected locked key removed, got %v", w.Canvas)
	}
}

// TestOps_GuardReport returns ok=true on a clean workflow.
func TestOps_GuardReport(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "guard"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	rep, err := ops.GuardReport(context.Background(), id)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if !rep.OK {
		t.Errorf("expected OK report, got %+v", rep)
	}
	if rep.ContentHash == "" {
		t.Error("expected content_hash to be populated")
	}
}

// TestOps_Versions covers list + detail + restore against a workflow
// that goes through create + publish + a second draft.
func TestOps_Versions(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "ver"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Publish(id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	w := sampleWF(id)
	w.Name = "edited"
	if err := svc.SaveDraft(id, w); err != nil {
		t.Fatalf("save edited: %v", err)
	}

	rows, err := ops.Versions(id)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if len(rows) < 3 {
		t.Errorf("expected ≥3 versions (initial draft + publish + edited draft), got %d", len(rows))
	}

	// First version was the initial scaffold draft.
	first := rows[len(rows)-1]
	if first.Kind != repository.KindDraft {
		t.Errorf("first kind: got %s want draft", first.Kind)
	}
	detail, err := ops.VersionDetail(first.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Body == "" {
		t.Error("detail body empty")
	}

	// Restore the first snapshot — name should revert.
	if _, err := ops.RestoreVersion(id, first.ID, "tester"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, _ := svc.LoadDraft(id)
	if got.Name != "wf-"+id {
		t.Errorf("restore: name got %q want %q", got.Name, "wf-"+id)
	}
}

// TestOps_DiffVersions returns two bodies belonging to the same
// workflow.
func TestOps_DiffVersions(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "diff"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := sampleWF(id)
	w.Name = "v2"
	if err := svc.SaveDraft(id, w); err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, _ := ops.Versions(id)
	if len(rows) < 2 {
		t.Fatalf("need ≥2 versions, got %d", len(rows))
	}
	// newest first → from = older, to = newer
	fromID := rows[len(rows)-1].ID
	toID := rows[0].ID
	diff, err := ops.DiffVersions(id, fromID, toID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff.From.ID != fromID || diff.To.ID != toID {
		t.Errorf("ids: from=%d to=%d want %d/%d", diff.From.ID, diff.To.ID, fromID, toID)
	}
	if diff.From.Body == diff.To.Body {
		t.Errorf("bodies should differ; both = %q", diff.From.Body)
	}
}

// TestOps_DiffVersions_MismatchedWorkflow rejects cross-workflow diff
// requests.
func TestOps_DiffVersions_MismatchedWorkflow(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	if err := svc.Create("a", sampleWF("a")); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := svc.Create("b", sampleWF("b")); err != nil {
		t.Fatalf("b: %v", err)
	}
	rowsA, _ := ops.Versions("a")
	rowsB, _ := ops.Versions("b")
	_, err := ops.DiffVersions("a", rowsA[0].ID, rowsB[0].ID)
	if err == nil {
		t.Error("expected error for cross-workflow diff")
	}
}

// TestOps_ExecNode renders a transform node in isolation.
func TestOps_ExecNode(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "exec"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	body := ExecNodeInput{
		Node: workflow.Node{
			ID:         "t1",
			Type:       workflow.NodeTransform,
			Engine:     "gotemplate",
			Expression: `hello {{index .Event.Payload "name"}}`,
		},
		Event: map[string]any{
			"type":    "manual",
			"payload": map[string]any{"name": "world"},
		},
	}
	resp, err := ops.ExecNode(context.Background(), id, body)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !resp["ok"].(bool) {
		t.Errorf("ok=false: %+v", resp)
	}
	out := resp["output"].(map[string]any)
	if got := out["result"]; got != "hello world" {
		t.Errorf("result: got %v want 'hello world'", got)
	}
}

// TestOps_ExecNode_UnknownType errors clearly when the engine has no
// executor for the requested type.
func TestOps_ExecNode_UnknownType(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	id := "exec-bad"
	if err := svc.Create(id, sampleWF(id)); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := ops.ExecNode(context.Background(), id, ExecNodeInput{
		Node: workflow.Node{ID: "x", Type: "made_up"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestOps_Versions_NoRepo errors when DB isn't wired.
func TestOps_Versions_NoRepo(t *testing.T) {
	ops := &Ops{} // no Repo
	if _, err := ops.Versions("x"); err == nil {
		t.Error("expected error when Repo nil")
	}
	if _, err := ops.VersionDetail(1); err == nil {
		t.Error("expected error when Repo nil")
	}
	if _, err := ops.RestoreVersion("x", 1, ""); err == nil {
		t.Error("expected error when Repo nil")
	}
	if _, err := ops.DiffVersions("x", 1, 2); err == nil {
		t.Error("expected error when Repo nil")
	}
}

// ── CanvasView ────────────────────────────────────────────────────────────────

func TestOps_CanvasView_TablesAndASCII(t *testing.T) {
	ops, svc := newOpsForExtras(t)

	// Create a workflow with two nodes and one edge.
	wf := workflow.Workflow{
		ID:      "cv-wf",
		Name:    "Canvas Test",
		Enabled: false,
		Triggers: []workflow.Trigger{
			{ID: "trig-m", Type: workflow.TriggerManual, EntryNode: "n1"},
		},
		Graph: workflow.Graph{
			Entry: "n1",
			Nodes: []workflow.Node{
				{ID: "n1", Type: workflow.NodeTransform, Label: "first", Engine: "gotemplate", Expression: "ok"},
				{ID: "n2", Type: workflow.NodeEnd, Label: "done"},
			},
			Edges: []workflow.Edge{{From: "n1", To: "n2"}},
		},
		Canvas: map[string]any{
			"positions": map[string]any{
				"n1":     map[string]any{"x": 100, "y": 200},
				"n2":     map[string]any{"x": 380, "y": 200},
				"trig-m": map[string]any{"x": 100, "y": 60},
			},
		},
	}
	if err := svc.Create("cv-wf", wf); err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := ops.CanvasView("cv-wf")
	if err != nil {
		t.Fatalf("CanvasView error: %v", err)
	}

	if result.Stats.NodeCount != 2 {
		t.Errorf("NodeCount: want 2, got %d", result.Stats.NodeCount)
	}
	if result.Stats.TriggerCount != 1 {
		t.Errorf("TriggerCount: want 1, got %d", result.Stats.TriggerCount)
	}
	if result.Stats.EdgeCount != 1 {
		t.Errorf("EdgeCount: want 1, got %d", result.Stats.EdgeCount)
	}
	if result.ASCII == "" {
		t.Error("expected non-empty ASCII output")
	}
	// n1 should have edge to n2.
	var n1Row *CanvasViewRow
	for i := range result.Nodes {
		if result.Nodes[i].ID == "n1" {
			n1Row = &result.Nodes[i]
			break
		}
	}
	if n1Row == nil {
		t.Fatal("n1 not found in Nodes")
	}
	if len(n1Row.EdgesTo) == 0 || n1Row.EdgesTo[0] != "n2" {
		t.Errorf("n1 edges: want [n2], got %v", n1Row.EdgesTo)
	}
	if n1Row.X != 100 || n1Row.Y != 200 {
		t.Errorf("n1 position: want 100,200 got %d,%d", n1Row.X, n1Row.Y)
	}
	// trigger row
	if len(result.Triggers) != 1 || result.Triggers[0].ID != "trig-m" {
		t.Errorf("triggers: want [trig-m], got %v", result.Triggers)
	}
}

func TestOps_CanvasView_NoService(t *testing.T) {
	ops := &Ops{}
	if _, err := ops.CanvasView("x"); err == nil {
		t.Error("expected error when Service nil")
	}
}

func TestOps_CanvasView_UnpositionedCounted(t *testing.T) {
	ops, svc := newOpsForExtras(t)
	wf := workflow.Workflow{
		ID:   "upos",
		Name: "upos",
		Triggers: []workflow.Trigger{
			{ID: "t", Type: workflow.TriggerManual, EntryNode: "n1"},
		},
		Graph: workflow.Graph{
			Entry: "n1",
			Nodes: []workflow.Node{
				{ID: "n1", Type: workflow.NodeEnd},
				{ID: "n2", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{From: "n1", To: "n2"}},
		},
		// No canvas positions — both nodes at 0,0.
	}
	if err := svc.Create("upos", wf); err != nil {
		t.Fatalf("Create: %v", err)
	}
	result, err := ops.CanvasView("upos")
	if err != nil {
		t.Fatalf("CanvasView: %v", err)
	}
	if result.Stats.Unpositioned != 2 {
		t.Errorf("Unpositioned: want 2, got %d", result.Stats.Unpositioned)
	}
}

// _ keep refs for the imports used in the file even when tests
// short-circuit on errors.
var _ = errors.New
