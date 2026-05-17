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
func (c *Canvas) AddNode(slug string, n workflow.Node) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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
func (c *Canvas) UpdateNode(slug, nodeID string, patch map[string]any) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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
func (c *Canvas) DeleteNode(slug, nodeID string) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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
func (c *Canvas) Connect(slug, fromID, toID, caseLabel string) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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
		w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: fromID, To: toID, Case: caseLabel})
		return nil
	})
}

// Disconnect removes an edge.
func (c *Canvas) Disconnect(slug, fromID, toID string) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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
func (c *Canvas) MoveNode(slug, nodeID string, x, y int) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
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

// SetTriggers replaces the trigger list.
func (c *Canvas) SetTriggers(slug string, triggers []workflow.Trigger) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
		w.Triggers = triggers
		return nil
	})
}

// Toggle flips enabled.
func (c *Canvas) Toggle(slug string, enabled bool) (workflow.Workflow, error) {
	return c.mutate(slug, func(w *workflow.Workflow) error {
		w.Enabled = enabled
		return nil
	})
}

func (c *Canvas) mutate(slug string, fn func(*workflow.Workflow) error) (workflow.Workflow, error) {
	w, err := c.Service.Load(slug)
	if err != nil {
		return workflow.Workflow{}, err
	}
	if err := fn(&w); err != nil {
		return workflow.Workflow{}, err
	}
	if r := parse.Validate(w); !r.Ok() {
		return workflow.Workflow{}, fmt.Errorf("post-edit validation failed: %s", r.Error())
	}
	if err := c.Service.Update(slug, w, nil); err != nil {
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
		"label": {}, "description": {}, "prompt": {}, "prompt_file": {},
		"timeout_sec": {}, "on_failure": {}, "fallback": {}, "provider": {},
		"preset": {}, "session": {}, "output_cases": {}, "expr": {},
		"url": {}, "method": {}, "channel": {}, "op": {}, "module": {},
		"row_id": {}, "args": {}, "command": {},
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
	if v, ok := patch["prompt_file"].(string); ok {
		n.PromptFile = v
	}
	if v, ok := patch["timeout_sec"].(int); ok {
		n.TimeoutSec = v
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
