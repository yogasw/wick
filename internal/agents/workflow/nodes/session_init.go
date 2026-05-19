package nodes

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// SessionInitExecutor implements the `session_init` node. It writes the
// resolved sessionID into rc.DefaultAgentSessionID so downstream agent
// nodes that don't override session: themselves inherit it, and (when a
// pool is wired) calls Pool.EnsureSession to materialize the sidebar
// row immediately.
//
// No subprocess is spawned here — pool.Send is what triggers the actual
// spawn, and that happens lazily on the first agent node downstream.
// session_init's only side effect is "establish the sessionID + sidebar
// entry early so the user sees it before the first send latency hits."
type SessionInitExecutor struct {
	Pool *pool.Pool
}

// NewSessionInitExecutor builds the executor. Pool may be nil when the
// workflow runtime is wired without an agent pool (tests, headless
// MCP); in that case the sessionID is still resolved + stored on
// RunContext, the pool side effect is skipped.
func NewSessionInitExecutor(p *pool.Pool) *SessionInitExecutor {
	return &SessionInitExecutor{Pool: p}
}

// Execute resolves the sessionID, mutates RunContext, and ensures the
// pool session record exists.
func (e *SessionInitExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	sessionID, err := resolveSessionInitID(n, rc)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	rc.DefaultAgentSessionID = sessionID
	if rc.AgentSessionIDs == nil {
		rc.AgentSessionIDs = map[string]string{}
	}
	rc.AgentSessionIDs[n.ID] = sessionID

	if e.Pool != nil {
		if err := e.Pool.EnsureSession(ctx, sessionID, "workflow", n.Workspace); err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("ensure session: %w", err)
		}
	}

	return workflow.NodeOutput{
		Result: sessionID,
		Fields: map[string]any{
			"session_id": sessionID,
			"preset":     n.Preset,
		},
	}, nil
}

// resolveSessionInitID picks the sessionID per node config. SessionID
// (template) wins over Preset when both are set.
func resolveSessionInitID(n workflow.Node, rc *workflow.RunContext) (string, error) {
	if n.SessionID != "" {
		rendered, err := template.Render(n.SessionID, rc.RenderCtx())
		if err != nil {
			return "", fmt.Errorf("render session_id: %w", err)
		}
		if rendered == "" {
			return "", fmt.Errorf("session_id template rendered to empty string")
		}
		return rendered, nil
	}
	preset := n.Preset
	if preset == "" {
		preset = workflow.SessionPresetWorkflowRun
	}
	switch preset {
	case workflow.SessionPresetWorkflowRun:
		return DefaultRunSessionID(rc.Workflow.ID, rc.RunID), nil
	case workflow.SessionPresetWorkflowGlobal:
		return fmt.Sprintf("wf_%s", rc.Workflow.ID), nil
	case workflow.SessionPresetNew:
		return "wf_adhoc_" + uuid.NewString(), nil
	}
	return "", fmt.Errorf("unknown session preset %q", preset)
}
