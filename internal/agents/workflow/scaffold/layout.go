package scaffold

import (
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// ApplyTopDownLayout assigns Canvas.positions for every node + every
// trigger phantom so the editor renders the graph stacked vertically
// (top → bottom). Without this the canvas falls back to Drawflow's
// default left → right which feels unnatural for newly-scaffolded
// workflows.
//
// Called from scaffold.Workflow on every newly-created workflow so the
// first render is correct regardless of which surface (UI form, MCP
// workflow_create, CLI) kicked off the create.
//
// The trigger canvas IDs follow the same convention as
// `internal/tools/agents/workflows_codec.go:triggerNodeID` — "trigger-<type>"
// for the first trigger of a type, "trigger-<type>-N" for subsequent
// duplicates — so the canvas wires the phantoms back to the same node
// id the layout placed.
func ApplyTopDownLayout(w workflow.Workflow) workflow.Workflow {
	const (
		yStep = 160
		xStep = 320
		topY  = 0
	)

	if len(w.Graph.Nodes) == 0 {
		return w
	}

	adj := make(map[string][]string, len(w.Graph.Nodes))
	for _, n := range w.Graph.Nodes {
		adj[n.ID] = nil
	}
	for _, e := range w.Graph.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}

	type triggerCol struct {
		triggerID string
		entry     string
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
	if len(cols) == 0 {
		entry := w.Graph.Entry
		if entry == "" && len(w.Graph.Nodes) > 0 {
			entry = w.Graph.Nodes[0].ID
		}
		cols = []triggerCol{{"__trigger__", entry}}
	}

	nCols := len(cols)
	totalWidth := nCols * xStep
	startX := xStep/2 + (xStep-totalWidth)/2
	if nCols == 1 {
		startX = xStep + xStep/2
	}

	colX := make([]int, nCols)
	for i := range cols {
		colX[i] = startX + i*xStep
	}

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
	for i, col := range cols {
		positions[col.triggerID] = map[string]any{"x": colX[i], "y": topY}
	}
	for _, n := range w.Graph.Nodes {
		p := nodePos[n.ID]
		positions[n.ID] = map[string]any{
			"x": colX[p.col],
			"y": topY + p.rank*yStep,
		}
	}

	if w.Canvas == nil {
		w.Canvas = map[string]any{}
	}
	w.Canvas["positions"] = positions
	return w
}
