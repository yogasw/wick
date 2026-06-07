package parse

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// hasErrorAt reports whether Validate flagged a publish-blocking Error
// whose Path contains the given substring.
func hasErrorAt(r *Result, pathSub string) bool {
	for _, e := range r.Errors {
		if strings.Contains(e.Path, pathSub) {
			return true
		}
	}
	return false
}

func hasWarningAt(r *Result, pathSub string) bool {
	for _, e := range r.Warnings {
		if strings.Contains(e.Path, pathSub) {
			return true
		}
	}
	return false
}

// Start/end nodes are not mandatory: a workflow with at least one trigger
// but an empty graph must publish cleanly (no errors).
func TestValidate_TriggerOnly_NoNodes_Publishes(t *testing.T) {
	w := workflow.Workflow{
		ID:       "wf-trigger-only",
		Name:     "trigger only",
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
	}
	r := Validate(w)
	if !r.Ok() {
		t.Fatalf("expected publishable workflow, got errors: %s", r.Error())
	}
}

// A dangling trigger entry_node (e.g. a scaffolded "start" node the user
// deleted) must NOT block publish — it degrades to a warning.
func TestValidate_DanglingEntryNode_WarnsNotBlocks(t *testing.T) {
	w := workflow.Workflow{
		ID:       "wf-dangling",
		Name:     "dangling entry",
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual, EntryNode: "start"}},
		Graph: workflow.Graph{
			Nodes: []workflow.Node{{ID: "real", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
	r := Validate(w)
	if !r.Ok() {
		t.Fatalf("dangling entry_node should not block publish, got: %s", r.Error())
	}
	if hasErrorAt(r, "entry_node") {
		t.Fatal("dangling entry_node surfaced as a blocking error")
	}
	if !hasWarningAt(r, "entry_node") {
		t.Fatal("expected a warning for the dangling entry_node")
	}
}

// A dangling graph.entry likewise warns rather than blocks.
func TestValidate_DanglingGraphEntry_WarnsNotBlocks(t *testing.T) {
	w := workflow.Workflow{
		ID:       "wf-dangling-entry",
		Name:     "dangling graph entry",
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "start",
			Nodes: []workflow.Node{{ID: "real", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
	r := Validate(w)
	if !r.Ok() {
		t.Fatalf("dangling graph.entry should not block publish, got: %s", r.Error())
	}
	if !hasWarningAt(r, "graph.entry") {
		t.Fatal("expected a warning for the dangling graph.entry")
	}
}

// The one hard publish requirement remains: at least one trigger.
func TestValidate_NoTrigger_StillBlocks(t *testing.T) {
	w := workflow.Workflow{ID: "wf-no-trigger", Name: "no trigger"}
	r := Validate(w)
	if !hasErrorAt(r, "triggers") {
		t.Fatal("a workflow with no triggers must still fail to publish")
	}
}
