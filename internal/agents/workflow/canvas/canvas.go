// Package canvas mutates a Workflow's graph atomically. Used by MCP
// canvas ops (`workflow_add_node`, `workflow_connect`, ...) and by
// the UI inspector when the operator edits in the visual editor.
//
// Each mutation returns the updated workflow so callers can persist
// it via service.Update. Mutations never partially apply — if
// validation fails post-edit, the workflow is returned unchanged.
package canvas

import (
	"encoding/json"
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
		return addNode(w, n)
	})
}

// UpdateNode merges a patch into an existing node.
func (c *Canvas) UpdateNode(id, nodeID string, patch map[string]any) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		return updateNode(w, nodeID, patch)
	})
}

// DeleteNode removes a node and every edge touching it.
func (c *Canvas) DeleteNode(id, nodeID string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		return deleteNode(w, nodeID)
	})
}

// Connect adds an edge.
func (c *Canvas) Connect(id, fromID, toID, caseLabel string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		return connect(w, fromID, toID, caseLabel)
	})
}

// Disconnect removes an edge.
func (c *Canvas) Disconnect(id, fromID, toID string) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		return disconnect(w, fromID, toID)
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
		return moveNodes(w, moves)
	})
}

// layout constants used by AutoLayout.
// Top-down lane layout: each trigger owns a column (lane), Y = depth
// level, X = parallel branches spread rightwards inside the lane.
const (
	layoutXGap    = 260 // horizontal gap between nodes in the same row of a lane
	layoutYGap    = 220 // vertical gap between depth levels
	layoutLaneGap = 200 // extra empty space between two lanes
	layoutXOrigin = 160 // X of the first lane's left edge
	layoutYOrigin = 60  // Y for the trigger row
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

// computeLayout returns top-down lane positions.
//
// Layout model:
//
//	triggers                → Y = layoutYOrigin (60), top of their lane
//	graph depth 0 (roots)   → Y = layoutYOrigin + layoutYGap (280)
//	graph depth N           → Y = layoutYOrigin + (N+1)*layoutYGap
//
// Lanes: walking the triggers in declared order, each trigger claims
// every node reachable from its entry that no earlier trigger claimed.
// So a node only one trigger reaches sits in that trigger's lane, and a
// node shared by several triggers sits in the FIRST trigger's lane.
// Triggers on the same entry share one lane. Nodes no trigger reaches
// (orphan roots, cycles) get lanes of their own after the trigger lanes.
// Lanes sit side by side left→right with layoutLaneGap between them, so
// two triggers' paths never stack on or cross each other.
//
// Inside a lane, a depth row with several nodes (parallel branches)
// spreads rightwards from the lane's left edge, sorted by ID.
//
// sticky_note nodes are never moved: they are annotations the author
// placed around a block by hand, and guessing their new box from the
// block's new layout is worse than leaving them for the author/AI to
// re-wrap with workflow_move_nodes.
//
// When restrict is non-empty only those node IDs are repositioned and
// trigger placement is skipped.
func computeLayout(w *workflow.Workflow, restrict []string) map[string]map[string]any {
	layoutAll := len(restrict) == 0

	annotation := make(map[string]bool)
	for _, n := range w.Graph.Nodes {
		if n.Type.IsAnnotation() {
			annotation[n.ID] = true
		}
	}

	// --- Build scope: graph nodes only (triggers placed separately) ---
	scope := make(map[string]bool)
	if layoutAll {
		for _, n := range w.Graph.Nodes {
			if !annotation[n.ID] {
				scope[n.ID] = true
			}
		}
	} else {
		for _, id := range restrict {
			if !annotation[id] {
				scope[id] = true
			}
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
	for id := range children {
		sort.Strings(children[id])
	}
	roots := make([]string, 0, len(scope))
	for id := range scope {
		if inbound[id] == 0 {
			roots = append(roots, id)
		}
	}
	sort.Strings(roots)

	// --- Kahn's BFS: depth = rows below the trigger row --------------
	depth := make(map[string]int, len(scope))
	for id := range scope {
		depth[id] = 0
	}
	pending := make(map[string]int, len(inbound))
	for id, n := range inbound {
		pending[id] = n
	}
	ready := append([]string(nil), roots...)
	visited := make(map[string]bool, len(scope))
	maxDepth := 0
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, child := range children[cur] {
			if d := depth[cur] + 1; d > depth[child] {
				depth[child] = d
				if d > maxDepth {
					maxDepth = d
				}
			}
			pending[child]--
			if pending[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	// Unreachable nodes (cycles) land after the deepest reachable row.
	cyclic := make([]string, 0)
	for id := range scope {
		if !visited[id] {
			cyclic = append(cyclic, id)
		}
	}
	sort.Strings(cyclic)
	for _, id := range cyclic {
		maxDepth++
		depth[id] = maxDepth
	}

	// --- Assign lanes -------------------------------------------------
	// lane[id] = index into lanes. claim walks every node reachable from
	// seed that has no lane yet and gives it lane l.
	lane := make(map[string]int, len(scope))
	laneCount := 0
	claim := func(seed string, l int) bool {
		if !scope[seed] {
			return false
		}
		if _, taken := lane[seed]; taken {
			return false
		}
		stack := []string{seed}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, taken := lane[cur]; taken {
				continue
			}
			lane[cur] = l
			stack = append(stack, children[cur]...)
		}
		return true
	}

	trigs := withTriggerIDs(w.Triggers)
	entryOf := func(t workflow.Trigger) string {
		if t.EntryNode != "" {
			return t.EntryNode
		}
		return w.Graph.Entry
	}
	trigLane := make(map[string]int, len(trigs))
	for _, t := range trigs {
		entry := entryOf(t)
		if l, ok := lane[entry]; ok {
			trigLane[t.ID] = l // entry already claimed: share that lane
			continue
		}
		if claim(entry, laneCount) {
			trigLane[t.ID] = laneCount
			laneCount++
			continue
		}
		trigLane[t.ID] = -1 // entry missing / out of scope
	}
	for _, id := range append(roots, cyclic...) {
		if claim(id, laneCount) {
			laneCount++
		}
	}

	// --- Lane widths: widest row (or trigger row) decides --------------
	rows := make(map[int]map[int][]string, laneCount) // lane → depth → ids
	for id, l := range lane {
		if rows[l] == nil {
			rows[l] = map[int][]string{}
		}
		rows[l][depth[id]] = append(rows[l][depth[id]], id)
	}
	trigsInLane := make(map[int][]string, laneCount)
	orphanTrigs := make([]string, 0)
	for _, t := range trigs {
		if l := trigLane[t.ID]; l >= 0 {
			trigsInLane[l] = append(trigsInLane[l], t.ID)
		} else {
			orphanTrigs = append(orphanTrigs, t.ID)
		}
	}
	laneX := make([]int, laneCount)
	x := layoutXOrigin
	for l := 0; l < laneCount; l++ {
		laneX[l] = x
		cols := 1
		if layoutAll && len(trigsInLane[l]) > cols {
			cols = len(trigsInLane[l])
		}
		for d := range rows[l] {
			sort.Strings(rows[l][d])
			if len(rows[l][d]) > cols {
				cols = len(rows[l][d])
			}
		}
		x += cols*layoutXGap + layoutLaneGap
	}

	// --- Assign graph node positions ---------------------------------
	out := make(map[string]map[string]any, len(scope)+len(trigs))
	for l := 0; l < laneCount; l++ {
		for d, ids := range rows[l] {
			y := layoutYOrigin + (d+1)*layoutYGap
			for i, id := range ids {
				out[id] = map[string]any{"x": laneX[l] + i*layoutXGap, "y": y}
			}
		}
	}

	// --- Triggers: top row of their lane ------------------------------
	// Lay out EVERY trigger, including any that still lack an id
	// (workflows written before SetTriggers started minting them).
	// Skipping those left their cards stacked at the canvas origin with
	// no edge to their entry node. A trigger whose entry is missing gets
	// a trailing column of its own so it never overlaps a lane.
	if layoutAll {
		for l := 0; l < laneCount; l++ {
			for i, id := range trigsInLane[l] {
				out[id] = map[string]any{"x": laneX[l] + i*layoutXGap, "y": layoutYOrigin}
			}
		}
		for i, id := range orphanTrigs {
			out[id] = map[string]any{"x": x + i*layoutXGap, "y": layoutYOrigin}
		}
	}
	return out
}

// SetTriggers replaces the trigger list.
//
// Triggers that arrive without an ID get one minted here. An ID is not
// cosmetic: the canvas keys its trigger cards, positions, run status and
// trigger→entry_node edges by it, so a list of id-less triggers collapses
// into duplicate keys and the editor refuses to render the graph at all.
// Callers that build triggers by hand (MCP `workflow_set_triggers`) would
// otherwise leave the workflow runnable but uneditable.
func (c *Canvas) SetTriggers(id string, triggers []workflow.Trigger) (workflow.Workflow, error) {
	return c.mutate(id, func(w *workflow.Workflow) error {
		w.Triggers = withTriggerIDs(triggers)
		return nil
	})
}

// withTriggerIDs fills in a stable, unique ID for every trigger that lacks
// one, leaving explicitly-set IDs untouched. Shape matches the UI's own
// scaffold: trigger-<type>, then trigger-<type>-2, -3, ... on collision.
func withTriggerIDs(triggers []workflow.Trigger) []workflow.Trigger {
	seen := map[string]bool{}
	for _, t := range triggers {
		if t.ID != "" {
			seen[t.ID] = true
		}
	}
	out := make([]workflow.Trigger, len(triggers))
	copy(out, triggers)
	for i := range out {
		if out[i].ID != "" {
			continue
		}
		typ := string(out[i].Type)
		if typ == "" {
			typ = "manual"
		}
		candidate := "trigger-" + typ
		for n := 2; seen[candidate]; n++ {
			candidate = fmt.Sprintf("trigger-%s-%d", typ, n)
		}
		seen[candidate] = true
		out[i].ID = candidate
	}
	return out
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
		"model": {}, "preset": {}, "session": {}, "output_cases": {}, "expr": {},
		"url": {}, "method": {}, "channel": {}, "op": {}, "module": {},
		"row_id": {}, "args": {}, "command": {},
		"expression": {}, "engine": {}, "result": {},
		"max_turns": {}, "skills": {}, "tools": {},
		// go_script body + sticky_note fields.
		"code": {}, "content": {}, "color": {}, "width": {}, "height": {}, "texts": {},
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
	if v, ok := patch["model"].(string); ok {
		n.Model = v
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
	if v, ok := patch["code"].(string); ok {
		n.Code = v
	}
	if v, ok := patch["content"].(string); ok {
		n.Content = v
	}
	if v, ok := patch["color"].(string); ok {
		n.Color = v
	}
	switch v := patch["width"].(type) {
	case int:
		n.Width = v
	case float64:
		n.Width = int(v)
	}
	switch v := patch["height"].(type) {
	case int:
		n.Height = v
	case float64:
		n.Height = int(v)
	}
	if v, ok := patch["texts"]; ok {
		// Round-trip through JSON: the patch arrives as []any of maps
		// from MCP, or already typed from Go callers.
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("texts: %w", err)
		}
		var texts []workflow.StickyText
		if err := json.Unmarshal(raw, &texts); err != nil {
			return fmt.Errorf("texts: want [{id,content,x,y,width,color,size}]: %w", err)
		}
		n.Texts = texts
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

func addNode(w *workflow.Workflow, n workflow.Node) error {
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
}

func updateNode(w *workflow.Workflow, nodeID string, patch map[string]any) error {
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
}

func deleteNode(w *workflow.Workflow, nodeID string) error {
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
}

func connect(w *workflow.Workflow, fromID, toID, caseLabel string) error {
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
}

func disconnect(w *workflow.Workflow, fromID, toID string) error {
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
}

func moveNodes(w *workflow.Workflow, moves []NodeMove) error {
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
}
