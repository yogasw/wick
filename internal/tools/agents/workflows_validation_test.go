package agents

import (
	"strings"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
)

// TestValidationPathUsesNodeID guards the contract the canvas relies
// on: validation error paths must be `graph.nodes[<id>]` (id, not
// numeric index) so the UI can hang per-node error badges. If
// validator drifts back to numeric paths the UI silently loses its
// per-node mapping.
func TestValidationPathUsesNodeID(t *testing.T) {
	w := wf.Workflow{
		ID: "t",
		Graph: wf.Graph{
			Entry: "broken-classify",
			Nodes: []wf.Node{
				{ID: "broken-classify", Type: wf.NodeClassify}, // no prompt
			},
		},
		Triggers: []wf.Trigger{{Type: wf.TriggerManual}},
	}
	r := parse.Validate(w)
	if r.Ok() {
		t.Fatalf("expected validation errors for empty-prompt classify, got none")
	}
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e.Path, "graph.nodes[broken-classify]") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error path to contain node id `broken-classify`, got %+v", r.Errors)
	}
}

// TestNodeIDFromPath unit-tests the helper that parses validation
// paths back into node ids for the UI payload.
func TestNodeIDFromPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"graph.nodes[foo].field", "foo"},
		{"graph.nodes[bar]", "bar"},
		{"graph.nodes[my-node-id].id", "my-node-id"},
		{"graph.entry", ""},
		{"triggers[0].schedule", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := nodeIDFromPath(c.in)
		if got != c.want {
			t.Errorf("nodeIDFromPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestValidationPayloadShape locks the JSON shape the canvas consumes
// — `by_node` keyed by node id, `global` for non-scoped violations.
func TestValidationPayloadShape(t *testing.T) {
	r := &parse.Result{
		Errors: []parse.Error{
			{Path: "graph.nodes[a].field", Message: "missing field"},
			{Path: "graph.entry", Message: "unknown entry"},
		},
	}
	p := validationPayload(r)
	byNode, _ := p["by_node"].(map[string][]string)
	if len(byNode["a"]) != 1 || byNode["a"][0] != "missing field" {
		t.Errorf("by_node[a] missing: %+v", byNode)
	}
	global, _ := p["global"].([]string)
	if len(global) != 1 || !strings.Contains(global[0], "unknown entry") {
		t.Errorf("global missing entry error: %+v", global)
	}
	if p["ok"] != false {
		t.Errorf("expected ok=false on errors")
	}
}
