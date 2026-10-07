package workflow

import (
	"strings"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	wfcanvas "github.com/yogasw/wick/internal/agents/workflow/canvas"
)

// TestPrepareApplyOps_RefusesDelete keeps the destructive gate meaningful:
// an admin who left workflow_delete_node off must not be able to delete
// through the non-destructive batch op.
func TestPrepareApplyOps_RefusesDelete(t *testing.T) {
	_, err := prepareApplyOps([]wfcanvas.EditOp{
		{Op: "add_node", Node: &wf.Node{ID: "a", Type: wf.NodeEnd}},
		{Op: "delete_node", NodeID: "old"},
	})
	if err == nil || !strings.Contains(err.Error(), "ops[1] delete_node") {
		t.Fatalf("expected ops[1] delete_node refusal, got %v", err)
	}
}

// TestPrepareApplyOps_ReportsMintedIDs: an id-less add_node gets an id, and
// the caller is told which one, so it can reference the node later.
func TestPrepareApplyOps_ReportsMintedIDs(t *testing.T) {
	ops := []wfcanvas.EditOp{
		{Op: "add_node", Node: &wf.Node{ID: "keep", Type: wf.NodeEnd}},
		{Op: "add_node", Node: &wf.Node{Label: "done", Type: wf.NodeEnd}},
	}
	minted, err := prepareApplyOps(ops)
	if err != nil {
		t.Fatalf("prepareApplyOps: %v", err)
	}
	if ops[0].Node.ID != "keep" {
		t.Errorf("explicit id was changed to %q", ops[0].Node.ID)
	}
	if len(minted) != 1 || minted[0].OpIndex != 1 || minted[0].Label != "done" || minted[0].ID == "" || minted[0].ID != ops[1].Node.ID {
		t.Errorf("unexpected minted report %+v (node id %q)", minted, ops[1].Node.ID)
	}
}
