// Package canvas mutates a Workflow's graph atomically. Used by MCP
// canvas ops (`workflow_add_node`, `workflow_connect`, ...) and by
// the UI inspector when the operator edits in the visual editor.
//
// Each mutation returns the updated workflow so callers can persist
// it via service.Update. Mutations never partially apply — if
// validation fails post-edit, the workflow is returned unchanged.
package canvas

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/service"
)

// Canvas wraps a Service for atomic graph edits.
type Canvas struct {
	Service service.Service
}

// New binds a Canvas to a Service.
func New(svc service.Service) *Canvas {
	return &Canvas{Service: svc}
}

// AddNode appends a node to the workflow.
func (c *Canvas) AddNode(id string, n workflow.Node) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		if err := parse.ValidateNodeID(n.ID); err != nil {
			return err
		}
		for _, existing := range w.Graph.Nodes {
			if existing.ID == n.ID {
				return fmt.Errorf("node %q already exists", n.ID)
			}
		}
		w.Graph.Nodes = append(w.Graph.Nodes, n)
		return nil
	})
}

// UpdateNode merges a patch into an existing node.
func (c *Canvas) UpdateNode(id, nodeID string, patch map[string]any) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		idx := -1
		for i, n := range w.Graph.Nodes {
			if n.ID == nodeID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("node %q not found", nodeID)
		}
		if err := applyNodePatch(&w.Graph.Nodes[idx], patch); err != nil {
			return err
		}
		return nil
	})
}

// DeleteNode removes a node and every edge touching it.
func (c *Canvas) DeleteNode(id, nodeID string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		if w.Graph.Entry == nodeID {
			return fmt.Errorf("cannot delete entry node %q — reassign graph.entry to another node first", nodeID)
		}
		idx := -1
		for i, n := range w.Graph.Nodes {
			if n.ID == nodeID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("node %q not found", nodeID)
		}
		w.Graph.Nodes = append(w.Graph.Nodes[:idx], w.Graph.Nodes[idx+1:]...)
		kept := w.Graph.Edges[:0]
		for _, e := range w.Graph.Edges {
			if e.From == nodeID || e.To == nodeID {
				continue
			}
			kept = append(kept, e)
		}
		w.Graph.Edges = kept
		return nil
	})
}

// Connect adds an edge.
func (c *Canvas) Connect(id, fromID, toID, caseLabel string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		nodes := indexNodes(w.Graph)
		from, ok := nodes[fromID]
		if !ok {
			return fmt.Errorf("from node %q not found", fromID)
		}
		if _, ok := nodes[toID]; !ok {
			return fmt.Errorf("to node %q not found", toID)
		}
		if caseLabel != "" && !from.Type.IsBranchSource() {
			return errors.New("case only valid on edges from classify/branch source")
		}
		for _, e := range w.Graph.Edges {
			if e.From == fromID && e.To == toID && e.Case == caseLabel {
				return fmt.Errorf("edge %s→%s (case=%q) already exists", fromID, toID, caseLabel)
			}
		}
		w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: fromID, To: toID, Case: caseLabel})
		return nil
	})
}

// Disconnect removes an edge.
func (c *Canvas) Disconnect(id, fromID, toID string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		kept := w.Graph.Edges[:0]
		removed := false
		for _, e := range w.Graph.Edges {
			if e.From == fromID && e.To == toID && !removed {
				removed = true
				continue
			}
			kept = append(kept, e)
		}
		if !removed {
			return fmt.Errorf("edge %s→%s not found", fromID, toID)
		}
		w.Graph.Edges = kept
		return nil
	})
}

// MoveNode updates canvas position metadata.
func (c *Canvas) MoveNode(id, nodeID string, x, y int) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		if w.Canvas == nil {
			w.Canvas = map[string]any{}
		}
		positions, _ := w.Canvas["positions"].(map[string]any)
		if positions == nil {
			positions = map[string]any{}
		}
		positions[nodeID] = map[string]any{"x": x, "y": y}
		w.Canvas["positions"] = positions
		return nil
	})
}

// NodeMove carries a new canvas position for one node.
type NodeMove struct {
	NodeID string `json:"node_id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
}

// MoveNodes batch-updates canvas positions in a single draft mutation.
// Cheaper than N serial MoveNode calls; avoids partial-update races.
func (c *Canvas) MoveNodes(id string, moves []NodeMove) (workflow.Workflow, error) {
	if len(moves) == 0 {
		return workflow.Workflow{}, errors.New("moves: at least one entry required")
	}
	return c.mutate(id, func(w *workflow.Workflow) error {
		if w.Canvas == nil {
			w.Canvas = map[string]any{}
		}
		positions, _ := w.Canvas["positions"].(map[string]any)
		if positions == nil {
			positions = map[string]any{}
		}
		for _, mv := range moves {
			if mv.NodeID == "" {
				return errors.New("move: node_id is required")
			}
			positions[mv.NodeID] = map[string]any{"x": mv.X, "y": mv.Y}
		}
		w.Canvas["positions"] = positions
		return nil
	})
}

// layout constants used by AutoLayout.
// Top-down layout: Y = depth level, X = horizontal spread within level.
const (
	layoutXGap    = 260 // horizontal gap between nodes in the same level
	layoutYGap    = 220 // vertical gap between depth levels
	layoutXOrigin = 420 // center X around which each level is spread
	layoutYOrigin = 60  // Y for depth 0 (triggers / root nodes)
)

// AutoLayout computes DAG-aware positions and applies them in one draft
// mutation. nodeIDs restricts re-layout to those IDs only; empty = lay
// out ALL graph nodes and triggers. Positions for IDs outside scope are
// preserved.
func (c *Canvas) AutoLayout(id string, nodeIDs []string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		newPos := computeLayout(w, nodeIDs)
		if w.Canvas == nil {
			w.Canvas = map[string]any{}
		}
		positions, _ := w.Canvas["positions"].(map[string]any)
		if positions == nil {
			positions = map[string]any{}
		}
		for k, v := range newPos {
			positions[k] = v
		}
		w.Canvas["positions"] = positions
		return nil
	})
}

// computeLayout returns top-down DAG positions.
//
// Layout model:
//
//	triggers                → Y = layoutYOrigin (60)
//	graph depth 0 (roots)   → Y = layoutYOrigin + layoutYGap (280)
//	graph depth 1           → Y = layoutYOrigin + 2*layoutYGap (500)
//	graph depth N           → Y = layoutYOrigin + (N+1)*layoutYGap
//
// Triggers are placed DIRECTLY ABOVE their entry node (same X).
// This guarantees no trigger-to-entry edge ever crosses another edge.
// Multiple triggers on the same entry node are spread symmetrically.
//
// Within each depth level graph nodes are spread horizontally and
// centred around layoutXOrigin, sorted by ID for determinism.
//
// When restrict is non-empty only those node IDs are repositioned and
// trigger placement is skipped.
func computeLayout(w *workflow.Workflow, restrict []string) map[string]map[string]any {
	layoutAll := len(restrict) == 0

	// --- Build scope: graph nodes only (triggers placed separately) ---
	scope := make(map[string]bool)
	if layoutAll {
		for _, n := range w.Graph.Nodes {
			scope[n.ID] = true
		}
	} else {
		for _, id := range restrict {
			scope[id] = true
		}
	}

	// --- Graph-only adjacency for BFS depth --------------------------
	children := make(map[string][]string, len(scope))
	inbound := make(map[string]int, len(scope))
	for id := range scope {
		children[id] = nil
		inbound[id] = 0
	}
	for _, e := range w.Graph.Edges {
		if scope[e.From] && scope[e.To] {
			children[e.From] = append(children[e.From], e.To)
			inbound[e.To]++
		}
	}

	// --- Kahn's BFS: depth = rows below the trigger row --------------
	// Initialise all depths to 0 so roots appear in byDepth map.
	depth := make(map[string]int, len(scope))
	for id := range scope {
		depth[id] = 0
	}
	ready := make([]string, 0, len(scope))
	for id := range scope {
		if inbound[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	visited := make(map[string]bool, len(scope))
	maxDepth := 0
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		ch := append([]string(nil), children[cur]...)
		sort.Strings(ch)
		for _, child := range ch {
			if d := depth[cur] + 1; d > depth[child] {
				depth[child] = d
				if d > maxDepth {
					maxDepth = d
				}
			}
			inbound[child]--
			if inbound[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	// Unreachable nodes (cycles) land after the deepest reachable row.
	for id := range scope {
		if !visited[id] {
			maxDepth++
			depth[id] = maxDepth
		}
	}

	// --- Group by depth, sort within level for determinism -----------
	byDepth := make(map[int][]string, maxDepth+1)
	for id, d := range depth {
		byDepth[d] = append(byDepth[d], id)
	}
	for d := range byDepth {
		sort.Strings(byDepth[d])
	}

	// --- Assign graph node positions ---------------------------------
	// depth 0 → Y = layoutYOrigin + layoutYGap  (280 default)
	// depth N → Y = layoutYOrigin + (N+1)*layoutYGap
	out := make(map[string]map[string]any, len(scope))
	for d := 0; d <= maxDepth; d++ {
		ids := byDepth[d]
		if len(ids) == 0 {
			continue
		}
		y := layoutYOrigin + (d+1)*layoutYGap
		totalW := (len(ids) - 1) * layoutXGap
		startX := layoutXOrigin - totalW/2
		for i, id := range ids {
			out[id] = map[string]any{
				"x": startX + i*layoutXGap,
				"y": y,
			}
		}
	}

	// --- Place triggers directly above their entry nodes -------------
	// Each trigger shares the X of its entry node (straight edge, no
	// crossing). Multiple triggers on the same entry are spread
	// symmetrically around that X.
	if layoutAll {
		byEntry := make(map[string][]workflow.Trigger)
		for _, t := range w.Triggers {
			if t.ID != "" {
				byEntry[t.EntryNode] = append(byEntry[t.EntryNode], t)
			}
		}
		for entryID, trigs := range byEntry {
			sort.Slice(trigs, func(i, j int) bool { return trigs[i].ID < trigs[j].ID })
			entryX := layoutXOrigin
			if pos, ok := out[entryID]; ok {
				if x, ok := pos["x"].(int); ok {
					entryX = x
				}
			}
			// Spread: centred on entryX, gap = layoutXGap between triggers.
			totalW := (len(trigs) - 1) * layoutXGap
			startX := entryX - totalW/2
			for i, t := range trigs {
				out[t.ID] = map[string]any{
					"x": startX + i*layoutXGap,
					"y": layoutYOrigin,
				}
			}
		}
	}
	return out
}

// SetTriggers replaces the trigger list.
func (c *Canvas) SetTriggers(id string, triggers []workflow.Trigger) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		w.Triggers = triggers
		return nil
	})
}

// Toggle flips enabled.
func (c *Canvas) Toggle(id string, enabled bool) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		w.Enabled = enabled
		return nil
	})
}

func (c *Canvas) mutate(id string, fn func(*workflow.Workflow) error) (workflow.Workflow, error) {
	// Read from draft if present so canvas edits (add_node, update_node,
	// connect, etc.) stack on top of in-progress draft edits rather than
	// reading stale published state and overwriting draft content.
	w, err := c.Service.LoadDraft(id)
	if err != nil {
		return workflow.Workflow{}, err
	}
	if err := fn(&w); err != nil {
		return workflow.Workflow{}, err
	}
	if r := parse.Validate(w); !r.Ok() {
		return workflow.Workflow{}, fmt.Errorf("post-edit validation failed: %s", r.Error())
	}
	if err := c.Service.SaveDraft(id, w); err != nil {
		return workflow.Workflow{}, err
	}
	return w, nil
}

func indexNodes(g workflow.Graph) map[string]workflow.Node {
	m := map[string]workflow.Node{}
	for _, n := range g.Nodes {
		m[n.ID] = n
	}
	return m
}

func applyNodePatch(n *workflow.Node, patch map[string]any) error {
	knownKeys := map[string]struct{}{
		"label": {}, "description": {}, "prompt": {},
		"timeout_sec": {}, "on_failure": {}, "fallback": {}, "provider": {},
		"preset": {}, "session": {}, "output_cases": {}, "expr": {},
		"url": {}, "method": {}, "channel": {}, "op": {}, "module": {},
		"row_id": {}, "args": {}, "command": {},
		"expression": {}, "engine": {}, "result": {},
		"max_turns": {}, "skills": {}, "tools": {},
	}
	var unknown []string
	for k := range patch {
		if _, ok := knownKeys[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown patch key(s): %s", strings.Join(unknown, ", "))
	}
	if v, ok := patch["label"].(string); ok {
		n.Label = v
	}
	if v, ok := patch["description"].(string); ok {
		n.Description = v
	}
	if v, ok := patch["prompt"].(string); ok {
		n.Prompt = v
	}
	switch v := patch["timeout_sec"].(type) {
	case int:
		n.TimeoutSec = v
	case float64:
		n.TimeoutSec = int(v)
	}
	if v, ok := patch["on_failure"].(string); ok {
		n.OnFailure = v
	}
	if v, ok := patch["fallback"].(string); ok {
		n.Fallback = v
	}
	if v, ok := patch["provider"].(string); ok {
		n.Provider = v
	}
	if v, ok := patch["preset"].(string); ok {
		n.Preset = v
	}
	if v, ok := patch["session"].(string); ok {
		n.Session = v
	}
	if v, ok := patch["output_cases"].([]any); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		n.OutputCases = out
	}
	if v, ok := patch["expr"].(string); ok {
		n.Expr = v
	}
	if v, ok := patch["url"].(string); ok {
		n.URL = v
	}
	if v, ok := patch["method"].(string); ok {
		n.Method = v
	}
	if v, ok := patch["channel"].(string); ok {
		n.ChannelName = v
	}
	if v, ok := patch["op"].(string); ok {
		n.Op = v
	}
	if v, ok := patch["module"].(string); ok {
		n.Module = v
	}
	if v, ok := patch["row_id"].(string); ok {
		n.Row = v
	}
	if v, ok := patch["expression"].(string); ok {
		n.Expression = v
	}
	if v, ok := patch["engine"].(string); ok {
		n.Engine = v
	}
	if v, ok := patch["result"].(string); ok {
		n.Result = v
	}
	switch v := patch["max_turns"].(type) {
	case int:
		n.MaxTurns = v
	case float64:
		n.MaxTurns = int(v)
	}
	if v, ok := patch["skills"].([]any); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		n.Skills = out
	}
	if v, ok := patch["tools"].([]any); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		n.Tools = out
	}
	if v, ok := patch["args"].(map[string]any); ok {
		n.Args = v
	}
	if v, ok := patch["command"].([]any); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		n.Command = out
	}
	return nil
}
