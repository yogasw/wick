package canvas

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// scaffoldStartID is the placeholder end node the empty scaffold ships
// with (scaffold.Build default case) so a brand-new workflow validates.
const scaffoldStartID = "start"

// pruneUnusedScaffoldStart drops the scaffold's placeholder "start" node
// once the workflow has been built around it and nothing uses it any
// more. Workflows assembled over MCP add their own entry nodes and point
// the triggers at them, which left a lone "END · start" card on every
// canvas that people then had to delete by hand.
//
// Removed only when ALL hold, so a "start" someone actually wired in is
// never touched:
//   - it is an end node with no edge in or out,
//   - no trigger enters at it, and no trigger falls back to graph.entry
//     while graph.entry is still "start",
//   - no other node references it (merge inputs, parallel branches,
//     fallback, session_from, or a template reading .Node.start),
//   - at least one other non-annotation node exists.
//
// Reports whether the node was removed.
func pruneUnusedScaffoldStart(w *workflow.Workflow) bool {
	idx := -1
	others := 0
	for i, n := range w.Graph.Nodes {
		if n.ID == scaffoldStartID {
			idx = i
			continue
		}
		if !n.Type.IsAnnotation() {
			others++
		}
	}
	if idx < 0 || others == 0 || w.Graph.Nodes[idx].Type != workflow.NodeEnd {
		return false
	}
	for _, e := range w.Graph.Edges {
		if e.From == scaffoldStartID || e.To == scaffoldStartID {
			return false
		}
	}
	for _, t := range w.Triggers {
		if t.EntryNode == scaffoldStartID {
			return false
		}
		if t.EntryNode == "" && w.Graph.Entry == scaffoldStartID {
			return false
		}
	}
	for _, n := range w.Graph.Nodes {
		if n.ID == scaffoldStartID {
			continue
		}
		if n.Fallback == scaffoldStartID || n.SessionFrom == scaffoldStartID {
			return false
		}
		for _, id := range n.Inputs {
			if id == scaffoldStartID {
				return false
			}
		}
		for _, id := range n.Branches {
			if id == scaffoldStartID {
				return false
			}
		}
		if raw, err := json.Marshal(n); err != nil || strings.Contains(string(raw), "Node."+scaffoldStartID) {
			return false
		}
	}

	w.Graph.Nodes = append(w.Graph.Nodes[:idx:idx], w.Graph.Nodes[idx+1:]...)
	if w.Graph.Entry == scaffoldStartID {
		w.Graph.Entry = ""
	}
	if pos, ok := w.Canvas["positions"].(map[string]any); ok {
		delete(pos, scaffoldStartID)
	}
	return true
}
