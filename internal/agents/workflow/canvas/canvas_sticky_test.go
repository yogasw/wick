package canvas

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestUpdateNode_GoScriptCodeSavedToDraft: `code` used to be rejected as
// an unknown patch key, so an AI could not edit a go_script body.
func TestUpdateNode_GoScriptCodeSavedToDraft(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "gs", Type: workflow.NodeGoScript, Code: "package main\nfunc main() {}"})
	svc.workflows["wf"] = w

	const code = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(`{}`) }"
	if _, err := newCanvas(svc).UpdateNode("wf", "gs", map[string]any{"code": code}); err != nil {
		t.Fatalf("UpdateNode code: %v", err)
	}
	got, _ := findNode(svc.workflows["wf"].Graph.Nodes, "gs")
	if got.Code != code {
		t.Fatalf("draft code = %q, want %q", got.Code, code)
	}

	// Validation after the edit still runs: an empty body is refused.
	if _, err := newCanvas(svc).UpdateNode("wf", "gs", map[string]any{"code": ""}); err == nil {
		t.Fatal("expected post-edit validation to reject empty go_script code")
	}
}

func TestUpdateNode_StickyNoteFields(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "note", Type: workflow.NodeStickyNote, Content: "# Lane A"})
	svc.workflows["wf"] = w

	_, err := newCanvas(svc).UpdateNode("wf", "note", map[string]any{
		"content": "# Lane A\nfetch + reply", "color": "blue", "width": float64(480), "height": float64(320),
	})
	if err != nil {
		t.Fatalf("UpdateNode sticky: %v", err)
	}
	got, _ := findNode(svc.workflows["wf"].Graph.Nodes, "note")
	if got.Content != "# Lane A\nfetch + reply" || got.Color != "blue" || got.Width != 480 || got.Height != 320 {
		t.Fatalf("sticky patch not applied: %+v", got)
	}

	if _, err := newCanvas(svc).UpdateNode("wf", "note", map[string]any{"color": "pink"}); err == nil {
		t.Fatal("expected unknown color to fail validation")
	}
}

func TestConnect_StickyNoteRejected(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "note", Type: workflow.NodeStickyNote, Content: "x"})
	svc.workflows["wf"] = w

	for _, pair := range [][2]string{{"start", "note"}, {"note", "start"}} {
		_, err := newCanvas(svc).Connect("wf", pair[0], pair[1], "")
		if err == nil || !strings.Contains(err.Error(), "sticky_note") {
			t.Fatalf("Connect %s→%s: want sticky_note error, got %v", pair[0], pair[1], err)
		}
	}
}

// texts arrives from MCP as []any of JSON objects.
func TestUpdateNode_StickyNoteTexts(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "note", Type: workflow.NodeStickyNote, Content: "# Lane A"})
	svc.workflows["wf"] = w

	_, err := newCanvas(svc).UpdateNode("wf", "note", map[string]any{"texts": []any{
		map[string]any{"id": "t1", "content": "# Lane A", "x": 0.03, "y": 0.02},
		map[string]any{"id": "t2", "content": "why", "x": 0.55, "y": 0.1, "width": 0.4, "color": "blue", "size": "lg"},
	}})
	if err != nil {
		t.Fatalf("UpdateNode texts: %v", err)
	}
	got, _ := findNode(svc.workflows["wf"].Graph.Nodes, "note")
	if len(got.Texts) != 2 || got.Texts[1].ID != "t2" || got.Texts[1].X != 0.55 || got.Texts[1].Width != 0.4 || got.Texts[1].Color != "blue" || got.Texts[1].Size != "lg" {
		t.Fatalf("texts not applied: %+v", got.Texts)
	}

	if _, err := newCanvas(svc).UpdateNode("wf", "note", map[string]any{"texts": "nope"}); err == nil {
		t.Fatal("expected malformed texts to fail")
	}
}
