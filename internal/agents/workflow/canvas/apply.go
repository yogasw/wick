package canvas

import (
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// EditOp is one step of a batch edit run by Apply. Op picks the step;
// only the fields that step reads are looked at, the rest are ignored.
//
//	add_node      Node
//	update_node   NodeID, Patch
//	delete_node   NodeID
//	connect       From, To, Case (Case only for classify/branch sources)
//	disconnect    From, To
//	move          Moves (graph nodes, sticky notes and trigger ids alike)
//	set_triggers  Triggers (replaces the whole list)
type EditOp struct {
	Op       string             `json:"op"`
	Node     *workflow.Node     `json:"node,omitempty"`
	NodeID   string             `json:"node_id,omitempty"`
	Patch    map[string]any     `json:"patch,omitempty"`
	From     string             `json:"from,omitempty"`
	To       string             `json:"to,omitempty"`
	Case     string             `json:"case,omitempty"`
	Moves    []NodeMove         `json:"moves,omitempty"`
	Triggers []workflow.Trigger `json:"triggers,omitempty"`
}

// Apply runs every op against ONE loaded draft and saves it once.
//
// The single-op methods each load, validate and save the draft, so a
// ten-step edit meant ten round trips — and, over MCP, ten copies of the
// whole workflow in the caller's context. Apply is the same steps in one
// mutation: ops run in order (a later op sees what an earlier one did, so
// add a node and connect it in the same batch), validation runs once on
// the end state, and nothing is saved unless every op succeeds.
func (c *Canvas) Apply(id string, ops []EditOp) (workflow.Workflow, error) {
	if len(ops) == 0 {
		return workflow.Workflow{}, errors.New("ops: at least one entry required")
	}
	return c.mutate(id, func(w *workflow.Workflow) error {
		for i, op := range ops {
			if err := applyOp(w, op); err != nil {
				return fmt.Errorf("ops[%d] %s: %w", i, op.Op, err)
			}
		}
		return nil
	})
}

func applyOp(w *workflow.Workflow, op EditOp) error {
	switch op.Op {
	case "add_node":
		if op.Node == nil {
			return errors.New("node is required")
		}
		return addNode(w, *op.Node)
	case "update_node":
		if op.NodeID == "" {
			return errors.New("node_id is required")
		}
		return updateNode(w, op.NodeID, op.Patch)
	case "delete_node":
		if op.NodeID == "" {
			return errors.New("node_id is required")
		}
		return deleteNode(w, op.NodeID)
	case "connect":
		return connect(w, op.From, op.To, op.Case)
	case "disconnect":
		return disconnect(w, op.From, op.To)
	case "move":
		if len(op.Moves) == 0 {
			return errors.New("moves: at least one entry required")
		}
		return moveNodes(w, op.Moves)
	case "set_triggers":
		w.Triggers = withTriggerIDs(op.Triggers)
		return nil
	default:
		return fmt.Errorf("unknown op %q (want add_node, update_node, delete_node, connect, disconnect, move, set_triggers)", op.Op)
	}
}
