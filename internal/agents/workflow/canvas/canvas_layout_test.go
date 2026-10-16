package canvas

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// multiTriggerWorkflow: two triggers with their own paths that meet in
// a shared tail, plus a parallel fork in lane A and a sticky note.
//
//	t_a → a1 → {a2, a3} → shared → done
//	t_b → b1 → b2 ──────→ shared
func multiTriggerWorkflow() *workflow.Workflow {
	n := func(id string) workflow.Node { return workflow.Node{ID: id, Type: workflow.NodeEnd} }
	return &workflow.Workflow{
		Triggers: []workflow.Trigger{
			{ID: "t_a", Type: workflow.TriggerManual, EntryNode: "a1"},
			{ID: "t_b", Type: workflow.TriggerWebhook, EntryNode: "b1"},
		},
		Graph: workflow.Graph{
			Entry: "a1",
			Nodes: []workflow.Node{
				n("a1"), n("a2"), n("a3"), n("b1"), n("b2"), n("shared"), n("done"),
				{ID: "note", Type: workflow.NodeStickyNote, Content: "# Lane A"},
			},
			Edges: []workflow.Edge{
				{From: "a1", To: "a2"}, {From: "a1", To: "a3"},
				{From: "a2", To: "shared"}, {From: "a3", To: "shared"},
				{From: "b1", To: "b2"}, {From: "b2", To: "shared"},
				{From: "shared", To: "done"},
			},
		},
	}
}

func posXY(t *testing.T, out map[string]map[string]any, id string) (int, int) {
	t.Helper()
	p, ok := out[id]
	if !ok {
		t.Fatalf("no position for %q", id)
	}
	return p["x"].(int), p["y"].(int)
}

func TestComputeLayout_MultiTriggerLanes(t *testing.T) {
	out := computeLayout(multiTriggerWorkflow(), nil)

	// No two cards share a slot.
	seen := map[[2]int]string{}
	for id := range out {
		x, y := posXY(t, out, id)
		if other, dup := seen[[2]int{x, y}]; dup {
			t.Fatalf("%s and %s stacked at (%d,%d)", id, other, x, y)
		}
		seen[[2]int{x, y}] = id
	}

	// Lane A (t_a + its own nodes + the shared tail) sits entirely left
	// of lane B, with at least one gap between them.
	laneA := []string{"t_a", "a1", "a2", "a3", "shared", "done"}
	laneB := []string{"t_b", "b1", "b2"}
	maxA := -1 << 31
	for _, id := range laneA {
		if x, _ := posXY(t, out, id); x > maxA {
			maxA = x
		}
	}
	for _, id := range laneB {
		if x, _ := posXY(t, out, id); x < maxA+layoutXGap+layoutLaneGap {
			t.Fatalf("lane B node %s at x=%d overlaps lane A (max x=%d)", id, x, maxA)
		}
	}

	// Top→bottom: each trigger above its entry, which sits in the same column.
	for trig, entry := range map[string]string{"t_a": "a1", "t_b": "b1"} {
		tx, ty := posXY(t, out, trig)
		ex, ey := posXY(t, out, entry)
		if tx != ex || ty >= ey {
			t.Fatalf("%s (%d,%d) not straight above %s (%d,%d)", trig, tx, ty, entry, ex, ey)
		}
	}

	// Parallel branches share a row inside lane A, side by side.
	x2, y2 := posXY(t, out, "a2")
	x3, y3 := posXY(t, out, "a3")
	if y2 != y3 || x3-x2 != layoutXGap {
		t.Fatalf("parallel a2(%d,%d) a3(%d,%d) not side by side", x2, y2, x3, y3)
	}

	// Sticky notes are left where the author put them.
	if _, moved := out["note"]; moved {
		t.Fatal("sticky_note must not be repositioned by auto layout")
	}
}

func TestComputeLayout_TriggersOnSameEntryShareLane(t *testing.T) {
	w := multiTriggerWorkflow()
	w.Triggers = append(w.Triggers, workflow.Trigger{ID: "t_a2", Type: workflow.TriggerCron, EntryNode: "a1"})
	out := computeLayout(w, nil)

	xa, ya := posXY(t, out, "t_a")
	xa2, ya2 := posXY(t, out, "t_a2")
	xb, _ := posXY(t, out, "b1")
	if ya != ya2 || xa2 != xa+layoutXGap {
		t.Fatalf("t_a2 (%d,%d) not beside t_a (%d,%d)", xa2, ya2, xa, ya)
	}
	if xb <= xa2 {
		t.Fatalf("lane B (x=%d) not right of the widened lane A trigger row (x=%d)", xb, xa2)
	}
}

// A switch fans its cases out under the matching port, the error path
// lands under the error port, and the widened switch card never overlaps
// the cards next to it.
func TestComputeLayout_ChildrenUnderTheirPort(t *testing.T) {
	end := func(id string) workflow.Node { return workflow.Node{ID: id, Type: workflow.NodeEnd} }
	w := &workflow.Workflow{
		Triggers: []workflow.Trigger{{ID: "t", Type: workflow.TriggerManual, EntryNode: "sw"}},
		Graph: workflow.Graph{
			Entry: "sw",
			Nodes: []workflow.Node{
				{ID: "sw", Type: workflow.NodeSwitch, OnFailure: "fallback", Fallback: "oops",
					Cases: []workflow.SwitchCase{{When: "x", Case: "approved"}, {When: "y", Case: "rejected"}}},
				end("ok"), end("no"), end("other"), end("oops"),
			},
			Edges: []workflow.Edge{
				{From: "sw", To: "ok", Case: "approved"},
				{From: "sw", To: "no", Case: "rejected"},
				{From: "sw", To: "other", Case: "default"},
			},
		},
	}
	widths := cardWidths(w)
	if widths["sw"] != 360 { // approved, rejected, default, error
		t.Fatalf("switch width = %d, want 360", widths["sw"])
	}
	out := computeLayout(w, nil)
	sx, _ := posXY(t, out, "sw")
	order := []string{"ok", "no", "other", "oops"}
	prevRight := -1 << 31
	for i, id := range order {
		x, _ := posXY(t, out, id)
		if x < prevRight+layoutMinGap {
			t.Fatalf("%s at x=%d overlaps the card before it (right=%d)", id, x, prevRight)
		}
		prevRight = x + widths[id]
		port := sx + (2*i+1)*360/8
		if c := x + widths[id]/2; i == 0 && c != port {
			t.Fatalf("%s centred at %d, want under its port at %d", id, c, port)
		}
	}
	if _, y := posXY(t, out, "oops"); y != layoutYOrigin+2*layoutYGap {
		t.Fatalf("fallback target not one row under its source (y=%d)", y)
	}
}

// A note that wrapped a lane follows it through auto layout, and its top
// sits a full title height above the first card, so the title (heading
// plus a wrapped summary) is never under the trigger.
func TestFitNotes_RewrapsLaneAndClearsTitle(t *testing.T) {
	w := multiTriggerWorkflow()
	for i := range w.Graph.Nodes {
		if w.Graph.Nodes[i].ID == "note" {
			w.Graph.Nodes[i].Content = "### Inquiry baru masuk\nSetiap inquiry baru di #integration-and-ops dicatat dan diberi penanda di thread-nya."
			w.Graph.Nodes[i].Width, w.Graph.Nodes[i].Height = 320, 700
		}
	}
	// Before: the note's title band is 40 high and lane A's cards sit
	// right under it, some outside lane B's note.
	old := map[string]any{
		"note": map[string]any{"x": 0, "y": 0},
		"t_a":  map[string]any{"x": 50, "y": 40},
		"a1":   map[string]any{"x": 50, "y": 200},
		"a2":   map[string]any{"x": 50, "y": 400},
		"b1":   map[string]any{"x": 900, "y": 200},
	}
	moved := computeLayout(w, nil)
	notes := fitNotes(w, old, moved)
	p, ok := notes["note"]
	if !ok {
		t.Fatal("the note around lane A was not re-wrapped")
	}
	nx, ny := p["x"].(int), p["y"].(int)
	var note workflow.Node
	for _, n := range w.Graph.Nodes {
		if n.ID == "note" {
			note = n
		}
	}
	title := noteTitleHeight(note.Content, note.Width, note.Height)
	if title < 100 {
		t.Fatalf("title band %d too small for a heading and a 3-line summary", title)
	}
	for _, id := range []string{"t_a", "a1", "a2"} {
		x, y := posXY(t, moved, id)
		if y < ny+title {
			t.Errorf("%s at y=%d covers the title (note y=%d, title %d)", id, y, ny, title)
		}
		if x < nx || x+layoutCardW > nx+note.Width || y+layoutCardH > ny+note.Height {
			t.Errorf("%s (%d,%d) outside the note (%d,%d %dx%d)", id, x, y, nx, ny, note.Width, note.Height)
		}
	}
	if note.Width < 320 {
		t.Errorf("note narrowed to %d", note.Width)
	}
}

// A note that covered no card, or none that moved, is left alone.
func TestFitNotes_LeavesLooseNotes(t *testing.T) {
	w := multiTriggerWorkflow()
	old := map[string]any{"note": map[string]any{"x": 5000, "y": 5000}, "a1": map[string]any{"x": 50, "y": 200}}
	if out := fitNotes(w, old, computeLayout(w, nil)); len(out) != 0 {
		t.Fatalf("loose note moved: %v", out)
	}
	if out := fitNotes(w, map[string]any{"note": map[string]any{"x": 0, "y": 0}, "a1": map[string]any{"x": 50, "y": 200}}, map[string]map[string]any{}); len(out) != 0 {
		t.Fatalf("note moved although none of its cards did: %v", out)
	}
}

// A node added without a position lands under the lowest card, a note
// to the right of everything: never on the canvas origin.
func TestAddNode_DefaultPosition(t *testing.T) {
	w := multiTriggerWorkflow()
	w.Canvas = map[string]any{"positions": map[string]any{
		"t_a":  map[string]any{"x": 160, "y": 60},
		"a1":   map[string]any{"x": 160, "y": 280},
		"done": map[string]any{"x": 160, "y": 1160},
		"b1":   map[string]any{"x": 800, "y": 280},
		"note": map[string]any{"x": 120, "y": -80},
	}}
	if err := addNode(w, workflow.Node{ID: "fresh", Type: workflow.NodeEnd}); err != nil {
		t.Fatal(err)
	}
	if err := addNode(w, workflow.Node{ID: "memo", Type: workflow.NodeStickyNote, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	pos := w.Canvas["positions"].(map[string]any)
	if got := pos["fresh"].(map[string]any); got["x"] != 160 || got["y"] != 1160+layoutYGap {
		t.Fatalf("fresh node at %v, want under done", got)
	}
	if got := pos["memo"].(map[string]any); got["x"].(int) <= 800+layoutCardW {
		t.Fatalf("note at %v overlaps the cards", got)
	}
	// An explicit position (e.g. a later move in the same apply) wins.
	pos["kept"] = map[string]any{"x": 1, "y": 2}
	if err := addNode(w, workflow.Node{ID: "kept", Type: workflow.NodeEnd}); err != nil {
		t.Fatal(err)
	}
	if got := pos["kept"].(map[string]any); got["x"] != 1 {
		t.Fatalf("existing position overwritten: %v", got)
	}
}
