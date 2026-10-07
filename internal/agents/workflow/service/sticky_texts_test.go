package service

import (
	"reflect"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// TestStickyTextsRoundTrip — sticky_note texts (relative x/y/width)
// survive save → draft → publish unchanged.
func TestStickyTextsRoundTrip(t *testing.T) {
	svc, _ := newTestService(t)
	w := makeWorkflow("wf")
	if err := svc.Create("wf", w); err != nil {
		t.Fatalf("create: %v", err)
	}
	texts := []wf.StickyText{
		{ID: "t1", Content: "# Lane A", X: 0.03, Y: 0.02, Width: 0.94},
		{ID: "t2", Content: "fetch → reply\n- why: SLA", X: 0.55, Y: 0.1, Width: 0.4, Color: "green", Size: "sm"},
	}
	w2 := w
	w2.Graph.Nodes = append(append([]wf.Node{}, w.Graph.Nodes...), wf.Node{ID: "note", Type: wf.NodeStickyNote, Width: 480, Height: 320, Texts: texts})
	if err := svc.SaveDraft("wf", w2); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	check := func(stage string, got wf.Workflow) {
		t.Helper()
		for _, n := range got.Graph.Nodes {
			if n.ID == "note" {
				if !reflect.DeepEqual(n.Texts, texts) {
					t.Fatalf("%s texts = %+v, want %+v", stage, n.Texts, texts)
				}
				return
			}
		}
		t.Fatalf("%s: note missing", stage)
	}
	d, err := svc.LoadDraft("wf")
	if err != nil {
		t.Fatalf("load draft: %v", err)
	}
	check("draft", d)
	if _, err := svc.Publish("wf", ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	p, err := svc.Load("wf")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	check("published", p)
}
