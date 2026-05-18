package workflow

import (
	"fmt"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// normalizeTriggerEntryNodes fills EntryNode on any trigger where it is
// empty, falling back to Graph.Entry. Prevents the trigger→graph
// disconnect that occurs when AI writes workflow.yaml without entry_node.
func normalizeTriggerEntryNodes(w *wf.Workflow) {
	if w.Graph.Entry == "" {
		return
	}
	for i := range w.Triggers {
		if w.Triggers[i].EntryNode == "" {
			w.Triggers[i].EntryNode = w.Graph.Entry
		}
	}
}

// topDownLayout assigns Canvas.positions for every node so the editor
// renders the graph stacked vertically (top → bottom) rather than
// Drawflow's default left → right.
//
// Multi-trigger workflows: each trigger gets its own column. Each
// trigger node ID follows the same convention as triggerNodeID in
// workflows_codec.go — "trigger-<type>" for the first, "trigger-<type>-N"
// for subsequent ones — so the canvas wires them correctly.
//
// Used right after workflow_create + workflow_write_file so AI-generated
// workflows show up readable in the canvas without the user having to
// drag every node into place.
func topDownLayout(w wf.Workflow) wf.Workflow {
	const (
		yStep   = 160
		xStep   = 320
		topY    = 0
	)

	if len(w.Graph.Nodes) == 0 {
		return w
	}

	// Build adjacency from edges.
	adj := make(map[string][]string, len(w.Graph.Nodes))
	for _, n := range w.Graph.Nodes {
		adj[n.ID] = nil
	}
	for _, e := range w.Graph.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}

	// Collect trigger entry nodes in order, deduped.
	// Each unique entry node defines a sub-graph column.
	type triggerCol struct {
		triggerID string // canvas trigger node id
		entry     string // graph entry node
	}
	cols := []triggerCol{}
	seenEntry := map[string]bool{}
	typeCount := map[string]int{}
	for _, t := range w.Triggers {
		typ := string(t.Type)
		if typ == "" {
			typ = "manual"
		}
		typeCount[typ]++
		idx := typeCount[typ] - 1
		var tid string
		if t.ID != "" {
			tid = t.ID
		} else if idx == 0 {
			tid = "trigger-" + typ
		} else {
			tid = fmt.Sprintf("trigger-%s-%d", typ, idx+1)
		}
		entry := t.EntryNode
		if entry == "" {
			entry = w.Graph.Entry
		}
		if entry != "" && !seenEntry[entry] {
			seenEntry[entry] = true
			cols = append(cols, triggerCol{triggerID: tid, entry: entry})
		}
	}
	// Fallback: single column from Graph.Entry.
	if len(cols) == 0 {
		entry := w.Graph.Entry
		if entry == "" && len(w.Graph.Nodes) > 0 {
			entry = w.Graph.Nodes[0].ID
		}
		cols = []triggerCol{{"__trigger__", entry}}
	}

	// Assign columns: each sub-graph gets an x centerline.
	// Total width = nCols * xStep; center the whole thing.
	nCols := len(cols)
	totalWidth := nCols * xStep
	startX := xStep/2 + (xStep-totalWidth)/2 // left edge of first col center
	if nCols == 1 {
		startX = xStep + xStep/2 // ~480 for xStep=320
	}

	colX := make([]int, nCols)
	for i := range cols {
		colX[i] = startX + i*xStep
	}

	// BFS per column to assign (col, rank) to each node.
	type pos struct{ col, rank int }
	nodePos := map[string]pos{}

	for ci, col := range cols {
		if col.entry == "" {
			continue
		}
		queue := []string{col.entry}
		nodePos[col.entry] = pos{ci, 1}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur] {
				if _, ok := nodePos[next]; ok {
					continue
				}
				nodePos[next] = pos{ci, nodePos[cur].rank + 1}
				queue = append(queue, next)
			}
		}
	}

	// Any orphan node not reached by any BFS: place in last col, max rank+1.
	maxRank := 0
	for _, p := range nodePos {
		if p.rank > maxRank {
			maxRank = p.rank
		}
	}
	for _, n := range w.Graph.Nodes {
		if _, ok := nodePos[n.ID]; !ok {
			maxRank++
			nodePos[n.ID] = pos{nCols - 1, maxRank}
		}
	}

	positions := map[string]any{}

	// Trigger nodes at row 0.
	for i, col := range cols {
		positions[col.triggerID] = map[string]any{"x": colX[i], "y": topY}
	}

	// Graph nodes by (col, rank).
	for _, n := range w.Graph.Nodes {
		p := nodePos[n.ID]
		positions[n.ID] = map[string]any{
			"x": colX[p.col],
			"y": topY + p.rank*yStep,
		}
	}

	w.Canvas = map[string]any{"positions": positions}
	return w
}

// triggerCanvasID returns the canvas node ID for trigger at index idx,
// matching the codec convention in workflows_codec.go:triggerNodeID.
func triggerCanvasID(t wf.Trigger, idx int) string {
	if t.ID != "" {
		return t.ID
	}
	typ := string(t.Type)
	if typ == "" {
		typ = "manual"
	}
	if idx == 0 {
		return "trigger-" + typ
	}
	return fmt.Sprintf("trigger-%s-%d", typ, idx+1)
}
