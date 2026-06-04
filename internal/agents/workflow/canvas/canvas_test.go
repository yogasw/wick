// Package canvas tests cover all 9 public methods of Canvas:
// AddNode, UpdateNode, DeleteNode, Connect, Disconnect, MoveNode,
// SetTriggers, Toggle, and error propagation through mutate.
package canvas

import (
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// ── stub service ──────────────────────────────────────────────────────────────

type stubService struct {
	workflows  map[string]workflow.Workflow
	loadErr    error
	updateErr  error
}

func newStub(ids ...string) *stubService {
	s := &stubService{workflows: map[string]workflow.Workflow{}}
	for _, id := range ids {
		s.workflows[id] = minimalWorkflow(id)
	}
	return s
}

// minimalWorkflow builds the smallest workflow that passes parse.Validate.
func minimalWorkflow(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Name:    id,
		Enabled: false,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, EntryNode: "start"},
		},
		Graph: workflow.Graph{
			Nodes: []workflow.Node{
				{ID: "start", Type: workflow.NodeShell, Command: []string{"echo", "hi"}},
			},
		},
	}
}

func (s *stubService) Load(id string) (workflow.Workflow, error) {
	if s.loadErr != nil {
		return workflow.Workflow{}, s.loadErr
	}
	w, ok := s.workflows[id]
	if !ok {
		return workflow.Workflow{}, errors.New("not found: " + id)
	}
	return w, nil
}

func (s *stubService) Update(id string, w workflow.Workflow) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.workflows[id] = w
	return nil
}

// Remaining interface methods — all no-ops.
func (s *stubService) List() ([]string, error)                          { return nil, nil }
func (s *stubService) FindByName(_, _ string) (string, error)           { return "", nil }
func (s *stubService) Create(_ string, _ workflow.Workflow) error       { return nil }
func (s *stubService) Delete(_ string) error                            { return nil }
func (s *stubService) Toggle(_ string, _ bool) error                    { return nil }
func (s *stubService) LoadDraft(id string) (workflow.Workflow, error)   { return s.Load(id) }
func (s *stubService) HasDraft(_ string) bool                           { return false }
func (s *stubService) SaveDraft(id string, w workflow.Workflow) error   { s.workflows[id] = w; return nil }
func (s *stubService) Publish(_ string) (workflow.Workflow, error)      { return workflow.Workflow{}, nil }
func (s *stubService) DiscardDraft(_ string) error                      { return nil }
func (s *stubService) ListTests(_ string) ([]string, error)             { return nil, nil }
func (s *stubService) GetTest(_, _ string) ([]byte, error)              { return nil, nil }
func (s *stubService) SaveTest(_, _ string, _ []byte) error             { return nil }
func (s *stubService) DeleteTest(_, _ string) error                     { return nil }
func (s *stubService) LoadState(_ string) (workflow.WorkflowState, error)      { return workflow.WorkflowState{}, nil }
func (s *stubService) SaveState(_ string, _ workflow.WorkflowState) error      { return nil }
func (s *stubService) LoadEnvValues(_ string) (map[string]string, error)       { return nil, nil }
func (s *stubService) SaveEnvValues(_ string, _ map[string]string) error       { return nil }
func (s *stubService) BaseDir() string                                         { return "" }

// ── helpers ───────────────────────────────────────────────────────────────────

func newCanvas(svc *stubService) *Canvas {
	return New(svc)
}

func findNode(nodes []workflow.Node, id string) (workflow.Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return workflow.Node{}, false
}

func hasEdge(edges []workflow.Edge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// ── AddNode ───────────────────────────────────────────────────────────────────

func TestAddNode_Valid(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	newNode := workflow.Node{ID: "step2", Type: workflow.NodeShell, Command: []string{"ls"}}
	got, err := c.AddNode("wf", newNode)
	if err != nil {
		t.Fatalf("AddNode error: %v", err)
	}
	if _, ok := findNode(got.Graph.Nodes, "step2"); !ok {
		t.Error("expected step2 in returned workflow nodes")
	}
	// Verify persisted.
	saved, _ := svc.Load("wf")
	if _, ok := findNode(saved.Graph.Nodes, "step2"); !ok {
		t.Error("expected step2 persisted in service")
	}
}

func TestAddNode_DuplicateID(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.AddNode("wf", workflow.Node{ID: "start", Type: workflow.NodeShell, Command: []string{"x"}})
	if err == nil {
		t.Fatal("expected error for duplicate node ID, got nil")
	}
}

func TestAddNode_InvalidID(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	// Node ID with uppercase is invalid per NodeIDRe = [a-z0-9_-]+
	_, err := c.AddNode("wf", workflow.Node{ID: "BAD ID!", Type: workflow.NodeShell, Command: []string{"x"}})
	if err == nil {
		t.Fatal("expected error for invalid node ID, got nil")
	}
}

func TestAddNode_EmptyID(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.AddNode("wf", workflow.Node{ID: "", Type: workflow.NodeShell, Command: []string{"x"}})
	if err == nil {
		t.Fatal("expected error for empty node ID, got nil")
	}
}

// ── UpdateNode ────────────────────────────────────────────────────────────────

func TestUpdateNode_KnownKey(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	patch := map[string]any{"label": "my_label"}
	got, err := c.UpdateNode("wf", "start", patch)
	if err != nil {
		t.Fatalf("UpdateNode error: %v", err)
	}
	n, ok := findNode(got.Graph.Nodes, "start")
	if !ok {
		t.Fatal("start node missing from result")
	}
	if n.Label != "my_label" {
		t.Errorf("expected label %q, got %q", "my label", n.Label)
	}
}

func TestUpdateNode_MultipleKeys(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	patch := map[string]any{
		"label":       "hello",
		"description": "desc text",
		"on_failure":  "skip",
	}
	got, err := c.UpdateNode("wf", "start", patch)
	if err != nil {
		t.Fatalf("UpdateNode error: %v", err)
	}
	n, _ := findNode(got.Graph.Nodes, "start")
	if n.Label != "hello" || n.Description != "desc text" || n.OnFailure != "skip" {
		t.Errorf("patch not fully applied: %+v", n)
	}
}

func TestUpdateNode_NotFound(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.UpdateNode("wf", "nonexistent", map[string]any{"label": "x"})
	if err == nil {
		t.Fatal("expected error for missing node, got nil")
	}
}

// ── DeleteNode ────────────────────────────────────────────────────────────────

func TestDeleteNode_RemovesNodeAndEdges(t *testing.T) {
	svc := newStub("wf")
	// Add a second node and an edge so we can verify edge pruning.
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	w.Graph.Edges = []workflow.Edge{{From: "start", To: "end"}}
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.DeleteNode("wf", "end")
	if err != nil {
		t.Fatalf("DeleteNode error: %v", err)
	}
	if _, ok := findNode(got.Graph.Nodes, "end"); ok {
		t.Error("expected end node removed")
	}
	if hasEdge(got.Graph.Edges, "start", "end") {
		t.Error("expected edge start→end removed")
	}
}

func TestDeleteNode_NotFound(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.DeleteNode("wf", "ghost")
	if err == nil {
		t.Fatal("expected error for missing node, got nil")
	}
}

// ── Connect ───────────────────────────────────────────────────────────────────

func TestConnect_ValidEdge(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.Connect("wf", "start", "end", "")
	if err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	if !hasEdge(got.Graph.Edges, "start", "end") {
		t.Error("expected edge start→end in result")
	}
}

func TestConnect_FromNotFound(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.Connect("wf", "ghost", "start", "")
	if err == nil {
		t.Fatal("expected error when from node not found")
	}
}

func TestConnect_ToNotFound(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.Connect("wf", "start", "ghost", "")
	if err == nil {
		t.Fatal("expected error when to node not found")
	}
}

func TestConnect_CaseOnNonBranchNode(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	// start is NodeShell — not a branch source, so case label must fail.
	_, err := c.Connect("wf", "start", "end", "yes")
	if err == nil {
		t.Fatal("expected error when case used on non-branch source")
	}
}

func TestConnect_CaseOnClassifyNode(t *testing.T) {
	svc := newStub("wf")
	// Build a classify-based workflow that validates properly.
	// classify needs outgoing edges with a "default" case.
	w := workflow.Workflow{
		ID:      "wf2",
		Name:    "wf2",
		Enabled: false,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, EntryNode: "cls"},
		},
		Graph: workflow.Graph{
			Nodes: []workflow.Node{
				{
					ID:          "cls",
					Type:        workflow.NodeClassify,
					Prompt:      "classify this",
					OutputCases: []string{"yes", "no"},
				},
				{ID: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{
				{From: "cls", To: "end", Case: "default"},
			},
		},
	}
	svc.workflows["wf2"] = w

	c := newCanvas(svc)

	// Adding a case edge from a classify node should succeed.
	got, err := c.Connect("wf2", "cls", "end", "yes")
	if err != nil {
		t.Fatalf("Connect classify with case error: %v", err)
	}
	found := false
	for _, e := range got.Graph.Edges {
		if e.From == "cls" && e.To == "end" && e.Case == "yes" {
			found = true
		}
	}
	if !found {
		t.Error("expected classify→end case=yes edge in result")
	}
}

// ── Disconnect ────────────────────────────────────────────────────────────────

func TestDisconnect_RemovesEdge(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	w.Graph.Edges = []workflow.Edge{{From: "start", To: "end"}}
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.Disconnect("wf", "start", "end")
	if err != nil {
		t.Fatalf("Disconnect error: %v", err)
	}
	if hasEdge(got.Graph.Edges, "start", "end") {
		t.Error("expected edge start→end removed after Disconnect")
	}
}

func TestDisconnect_EdgeNotFound(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.Disconnect("wf", "start", "ghost")
	if err == nil {
		t.Fatal("expected error when edge not found")
	}
}

// ── MoveNode ──────────────────────────────────────────────────────────────────

func TestMoveNode_StoresPosition(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	got, err := c.MoveNode("wf", "start", 100, 200)
	if err != nil {
		t.Fatalf("MoveNode error: %v", err)
	}
	if got.Canvas == nil {
		t.Fatal("expected Canvas map to be non-nil")
	}
	positions, ok := got.Canvas["positions"].(map[string]any)
	if !ok {
		t.Fatalf("expected positions map, got %T", got.Canvas["positions"])
	}
	pos, ok := positions["start"].(map[string]any)
	if !ok {
		t.Fatalf("expected start position map, got %T", positions["start"])
	}
	if pos["x"] != 100 || pos["y"] != 200 {
		t.Errorf("expected x=100 y=200, got %v", pos)
	}
}

func TestMoveNode_WorksWhenCanvasNil(t *testing.T) {
	svc := newStub("wf")
	// Ensure Canvas is nil initially.
	w := svc.workflows["wf"]
	w.Canvas = nil
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.MoveNode("wf", "start", 10, 20)
	if err != nil {
		t.Fatalf("MoveNode error when Canvas nil: %v", err)
	}
	if got.Canvas == nil {
		t.Fatal("expected Canvas initialised")
	}
}

func TestMoveNode_UpdatesExistingPosition(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	if _, err := c.MoveNode("wf", "start", 0, 0); err != nil {
		t.Fatalf("first MoveNode: %v", err)
	}
	got, err := c.MoveNode("wf", "start", 50, 60)
	if err != nil {
		t.Fatalf("second MoveNode: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)
	pos := positions["start"].(map[string]any)
	if pos["x"] != 50 || pos["y"] != 60 {
		t.Errorf("expected updated x=50 y=60, got %v", pos)
	}
}

// ── SetTriggers ───────────────────────────────────────────────────────────────

func TestSetTriggers_ReplacesTriggers(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	newTriggers := []workflow.Trigger{
		{Type: workflow.TriggerManual, EntryNode: "start"},
		{Type: workflow.TriggerCron, Schedule: "0 * * * *", EntryNode: "start"},
	}
	got, err := c.SetTriggers("wf", newTriggers)
	if err != nil {
		t.Fatalf("SetTriggers error: %v", err)
	}
	if len(got.Triggers) != 2 {
		t.Errorf("expected 2 triggers, got %d", len(got.Triggers))
	}
	if got.Triggers[1].Type != workflow.TriggerCron {
		t.Errorf("expected second trigger to be cron")
	}
}

func TestSetTriggers_PersistsInService(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	triggers := []workflow.Trigger{{Type: workflow.TriggerManual, EntryNode: "start"}}
	if _, err := c.SetTriggers("wf", triggers); err != nil {
		t.Fatalf("SetTriggers: %v", err)
	}
	saved, _ := svc.Load("wf")
	if len(saved.Triggers) != 1 {
		t.Errorf("expected 1 trigger persisted, got %d", len(saved.Triggers))
	}
}

// ── Toggle ────────────────────────────────────────────────────────────────────

func TestToggle_EnablesWorkflow(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	got, err := c.Toggle("wf", true)
	if err != nil {
		t.Fatalf("Toggle enable error: %v", err)
	}
	if !got.Enabled {
		t.Error("expected Enabled = true")
	}
	saved, _ := svc.Load("wf")
	if !saved.Enabled {
		t.Error("expected Enabled persisted as true")
	}
}

func TestToggle_DisablesWorkflow(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Enabled = true
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.Toggle("wf", false)
	if err != nil {
		t.Fatalf("Toggle disable error: %v", err)
	}
	if got.Enabled {
		t.Error("expected Enabled = false")
	}
}

func TestToggle_FlipsValue(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	got1, _ := c.Toggle("wf", true)
	if !got1.Enabled {
		t.Error("expected true after first toggle")
	}
	got2, _ := c.Toggle("wf", false)
	if got2.Enabled {
		t.Error("expected false after second toggle")
	}
}

// ── mutate error propagation ───────────────────────────────────────────────────

func TestMutate_LoadFail_PropagatesError(t *testing.T) {
	svc := newStub()
	svc.loadErr = errors.New("storage unavailable")
	c := newCanvas(svc)

	_, err := c.Toggle("wf", true)
	if err == nil {
		t.Fatal("expected error when Load fails")
	}
	if err.Error() != "storage unavailable" {
		t.Errorf("expected propagated load error, got %v", err)
	}
}

func TestMutate_ValidationFail_WorkflowUnchanged(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	// SetTriggers with empty slice removes all triggers → post-edit
	// validation fails ("at least one trigger required").
	original, _ := svc.Load("wf")

	_, err := c.SetTriggers("wf", []workflow.Trigger{})
	if err == nil {
		t.Fatal("expected validation error when triggers empty")
	}

	// Service must remain unchanged.
	after, _ := svc.Load("wf")
	if len(after.Triggers) != len(original.Triggers) {
		t.Errorf("service state mutated despite failed validation: got %d triggers, want %d",
			len(after.Triggers), len(original.Triggers))
	}
}

func TestMutate_LoadMissingID_Error(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	// "other" is not seeded in the stub.
	_, err := c.AddNode("other", workflow.Node{ID: "x", Type: workflow.NodeShell, Command: []string{"ls"}})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

// ── MoveNodes (batch) ────────────────────────────────────────────────────────

func TestMoveNodes_BatchUpdatesPositions(t *testing.T) {
	svc := newStub("wf")
	// Add a second node so both IDs exist in the workflow.
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "n2", Type: workflow.NodeEnd})
	w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: "start", To: "n2"})
	svc.workflows["wf"] = w

	c := newCanvas(svc)
	moves := []NodeMove{
		{NodeID: "start", X: 100, Y: 200},
		{NodeID: "n2", X: 400, Y: 200},
	}
	got, err := c.MoveNodes("wf", moves)
	if err != nil {
		t.Fatalf("MoveNodes error: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)
	for _, mv := range moves {
		pos := positions[mv.NodeID].(map[string]any)
		if pos["x"] != mv.X || pos["y"] != mv.Y {
			t.Errorf("node %s: want x=%d y=%d, got %v", mv.NodeID, mv.X, mv.Y, pos)
		}
	}
}

func TestMoveNodes_EmptyMovesError(t *testing.T) {
	c := newCanvas(newStub("wf"))
	_, err := c.MoveNodes("wf", nil)
	if err == nil {
		t.Fatal("expected error for empty moves slice")
	}
}

func TestMoveNodes_MissingNodeIDError(t *testing.T) {
	c := newCanvas(newStub("wf"))
	_, err := c.MoveNodes("wf", []NodeMove{{NodeID: "", X: 10, Y: 10}})
	if err == nil {
		t.Fatal("expected error for empty node_id")
	}
}

func TestMoveNodes_PreservesUnlistedPositions(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)
	// Set start at known position first.
	if _, err := c.MoveNode("wf", "start", 50, 50); err != nil {
		t.Fatalf("setup MoveNode: %v", err)
	}
	// MoveNodes with a synthetic ID — start must be untouched.
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "n2", Type: workflow.NodeEnd})
	w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: "start", To: "n2"})
	svc.workflows["wf"] = w

	got, err := c.MoveNodes("wf", []NodeMove{{NodeID: "n2", X: 200, Y: 300}})
	if err != nil {
		t.Fatalf("MoveNodes: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)
	startPos := positions["start"].(map[string]any)
	if startPos["x"] != 50 || startPos["y"] != 50 {
		t.Errorf("start position changed unexpectedly: %v", startPos)
	}
}

// ── AutoLayout ────────────────────────────────────────────────────────────────

// chainWorkflow builds wf with a linear A→B→C chain.
func chainWorkflow(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Name:    id,
		Enabled: false,
		Triggers: []workflow.Trigger{
			{ID: "trig-manual", Type: workflow.TriggerManual, EntryNode: "a"},
		},
		Graph: workflow.Graph{
			Entry: "a",
			Nodes: []workflow.Node{
				{ID: "a", Type: workflow.NodeShell, Command: []string{"echo", "a"}},
				{ID: "b", Type: workflow.NodeShell, Command: []string{"echo", "b"}},
				{ID: "c", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{
				{From: "a", To: "b"},
				{From: "b", To: "c"},
			},
		},
	}
}

func TestAutoLayout_LinearChainIncreasingX(t *testing.T) {
	svc := &stubService{workflows: map[string]workflow.Workflow{"wf": chainWorkflow("wf")}}
	c := newCanvas(svc)

	got, err := c.AutoLayout("wf", nil)
	if err != nil {
		t.Fatalf("AutoLayout error: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)

	posFor := func(id string) (x, y int) {
		p, ok := positions[id].(map[string]any)
		if !ok {
			t.Fatalf("position for %q not found (keys: %v)", id, func() []string {
				var ks []string
				for k := range positions {
					ks = append(ks, k)
				}
				return ks
			}())
		}
		x = p["x"].(int)
		y = p["y"].(int)
		return
	}
	// Top-down layout: a→b→c means a.Y < b.Y < c.Y (increasing depth).
	// Each level has one node, so they're all centred at the same X.
	_, ay := posFor("a")
	_, by := posFor("b")
	_, cy := posFor("c")
	if !(ay < by && by < cy) {
		t.Errorf("expected a.Y < b.Y < c.Y (top-down), got %d %d %d", ay, by, cy)
	}
}

func TestAutoLayout_TriggerPlacedAboveEntryNode(t *testing.T) {
	svc := &stubService{workflows: map[string]workflow.Workflow{"wf": chainWorkflow("wf")}}
	c := newCanvas(svc)

	got, err := c.AutoLayout("wf", nil)
	if err != nil {
		t.Fatalf("AutoLayout error: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)

	tpos, ok := positions["trig-manual"].(map[string]any)
	if !ok {
		t.Fatal("trigger position not set")
	}
	if tpos["y"].(int) != layoutYOrigin {
		t.Errorf("trigger Y: want %d (layoutYOrigin), got %d", layoutYOrigin, tpos["y"].(int))
	}
	// Trigger should align horizontally with entry node "a".
	apos := positions["a"].(map[string]any)
	if tpos["x"].(int) != apos["x"].(int) {
		t.Errorf("trigger X %d != entry node X %d", tpos["x"].(int), apos["x"].(int))
	}
}

func TestAutoLayout_ScopedOnlyMovesListed(t *testing.T) {
	svc := &stubService{workflows: map[string]workflow.Workflow{"wf": chainWorkflow("wf")}}
	c := newCanvas(svc)

	// Pre-position node "c" at a known spot.
	if _, err := c.MoveNode("wf", "c", 999, 999); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Auto-layout only "a" and "b" — "c" must remain at 999,999.
	got, err := c.AutoLayout("wf", []string{"a", "b"})
	if err != nil {
		t.Fatalf("AutoLayout scoped error: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)
	cpos := positions["c"].(map[string]any)
	if cpos["x"].(int) != 999 || cpos["y"].(int) != 999 {
		t.Errorf("node c unexpectedly moved: %v", cpos)
	}
}

func TestAutoLayout_SameDepthSameY(t *testing.T) {
	// Two root nodes (no incoming edges) → both depth 0 → same Y, different X.
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes,
		workflow.Node{ID: "r1", Type: workflow.NodeEnd},
		workflow.Node{ID: "r2", Type: workflow.NodeEnd},
	)
	svc.workflows["wf"] = w

	c := newCanvas(svc)
	got, err := c.AutoLayout("wf", []string{"r1", "r2"})
	if err != nil {
		t.Fatalf("AutoLayout error: %v", err)
	}
	positions := got.Canvas["positions"].(map[string]any)
	r1 := positions["r1"].(map[string]any)
	r2 := positions["r2"].(map[string]any)
	// Same depth → same Y row.
	if r1["y"].(int) != r2["y"].(int) {
		t.Errorf("same-depth nodes should share Y: r1=%v r2=%v", r1, r2)
	}
	// Different horizontal position within the row.
	if r1["x"].(int) == r2["x"].(int) {
		t.Errorf("same-depth nodes should differ in X: r1=%v r2=%v", r1, r2)
	}
}
