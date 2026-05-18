package workflow

import (
	"context"
	"fmt"
)

// NodeOutput is what an executor returns. It is stored in
// state.Outputs[node.ID] and surfaced via `{{.Node.<id>.X}}`. The
// `Verdict` field is the routing key for classify/branch nodes.
type NodeOutput struct {
	Verdict    string         `json:"verdict,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Reasoning  string         `json:"reasoning,omitempty"`
	Result     any            `json:"result,omitempty"`
	Fields     map[string]any `json:"-"` // merged into top-level when serialized
}

// Executor runs one node body. Engine resolves the executor by
// node.Type and dispatches with the rendered context.
type Executor interface {
	Execute(ctx context.Context, node Node, rctx *RunContext) (NodeOutput, error)
}

// RunContext carries per-run state into executors. It's a thin
// pointer-receiver wrapper so executors can read outputs from
// upstream nodes + read env/secrets without taking the entire engine.
type RunContext struct {
	Workflow    Workflow
	Event       Event
	Outputs     map[string]any
	EnvValues   map[string]string
	Secrets     map[string]string
	RunID       string
	NodeOutputs map[string]NodeOutput

	// TriggerNodeID is the canvas/yaml node id under which the run's
	// firing trigger surfaces in {{.Node.<id>.…}}. Set by the engine
	// when pickEntry resolves a trigger row (uses Trigger.ID when set,
	// else falls back to the entry node id). Empty for legacy runs
	// (graph.entry fallback) — those still expose data via .Event.
	TriggerNodeID string

	// DefaultAgentSessionID is set by an upstream `session_init` node
	// and consumed by downstream `agent` nodes that don't override
	// session: themselves. Empty = engine falls back to the per-run
	// pattern "wf:<id>:run:<runID>". See pool.md for the resolver
	// order.
	DefaultAgentSessionID string

	// AgentSessionIDs maps node ID → resolved sessionID for every
	// agent / session_init node that has run. Lets downstream agent
	// nodes opt into "reuse this upstream's subprocess" via
	// `session_from: <node-id>` without re-resolving the template.
	AgentSessionIDs map[string]string
}

// RenderCtx materializes a RenderCtx from the RunContext for template
// rendering inside an executor.
func (r *RunContext) RenderCtx() RenderCtx {
	// labelByID lets template refs use either the stable id or the
	// user-facing label. Both keys point at the same payload map so
	// `{{.Node.<id>.x}}` and `{{.Node.<label>.x}}` resolve interchangeably
	// — UI defaults to label (what the operator typed), legacy refs and
	// internal session_from lookups still find data by id.
	labelByID := map[string]string{}
	for _, n := range r.Workflow.Graph.Nodes {
		if n.Label != "" && n.Label != n.ID {
			labelByID[n.ID] = n.Label
		}
	}
	for _, tr := range r.Workflow.Triggers {
		if tr.Label != "" && tr.ID != "" && tr.Label != tr.ID {
			labelByID[tr.ID] = tr.Label
		}
	}
	nodeMap := map[string]any{}
	for id, out := range r.NodeOutputs {
		m := map[string]any{}
		if out.Verdict != "" {
			m["verdict"] = out.Verdict
		}
		if out.Confidence != 0 {
			m["confidence"] = out.Confidence
		}
		if out.Reasoning != "" {
			m["reasoning"] = out.Reasoning
		}
		if out.Result != nil {
			m["result"] = out.Result
		}
		for k, v := range out.Fields {
			m[k] = v
		}
		nodeMap[id] = m
		if lbl, ok := labelByID[id]; ok {
			nodeMap[lbl] = m
		}
	}
	if r.TriggerNodeID != "" {
		trMap := map[string]any{
			"type":    r.Event.Type,
			"subtype": r.Event.Subtype,
			"channel": r.Event.Channel,
			"at":      r.Event.At,
			"payload": r.Event.Payload,
		}
		nodeMap[r.TriggerNodeID] = trMap
		if lbl, ok := labelByID[r.TriggerNodeID]; ok {
			nodeMap[lbl] = trMap
		}
	}
	return RenderCtx{
		Event:  r.Event,
		Node:   nodeMap,
		Env:    r.EnvValues,
		Secret: r.Secrets,
		Workflow: WorkflowRef{
			ID:      r.Workflow.ID,
			Version: r.Workflow.Version,
			Name:    r.Workflow.Name,
		},
		Run: RunRef{ID: r.RunID},
	}
}

// ExecError wraps an executor failure with node identity. Engine
// promotes this to state.Error.
type ExecError struct {
	Node    string
	Type    NodeType
	Wrapped error
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("node %s (%s): %v", e.Node, e.Type, e.Wrapped)
}

func (e *ExecError) Unwrap() error { return e.Wrapped }

// RenderCtx is the root object exposed to Go templates inside node
// bodies. Fields map 1:1 to the design refs:
//
//	{{.Event.X}}        — trigger event payload
//	{{.Node.<id>.X}}    — output of completed node X
//	{{.Env.X}}          — non-secret workflow env value
//	{{.Secret.X}}       — encrypted secret, decrypted on lookup
//	{{.Workflow.X}}     — workflow metadata
//	{{.Run.X}}          — runtime metadata
//	{{.Dataset.<alias>}} — dataset binding from datasets: list
type RenderCtx struct {
	Event    Event
	Node     map[string]any
	Env      map[string]string
	Secret   map[string]string
	Workflow WorkflowRef
	Run      RunRef
	Dataset  map[string]any
}

// WorkflowRef is the small subset of Workflow accessible to templates.
type WorkflowRef struct {
	ID      string
	Version int
	Name    string
}

// RunRef carries runtime metadata for templates.
type RunRef struct {
	ID        string
	StartedAt string
}
