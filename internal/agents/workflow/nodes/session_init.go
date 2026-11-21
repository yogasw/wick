package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
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

// Descriptor surfaces session_init in the editor palette and MCP
// catalog. No schema yet — session_init's fields are declared inline
// on the workflow.Node (mode, session_id) rather than via a tagged
// struct.
func (e *SessionInitExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Category:    engine.CategoryAI,
		Label:       "Session Init",
		Badge:       "session",
		Description: "Resolve the AI session id early so downstream agent nodes share it and the sidebar row appears before the first turn.",
		WhenToUse:   "Workflow uses agent nodes and you want the session sidebar entry visible immediately, or you want to pin a specific session id. Set provider (\"type/name\") and optionally model to pin the session to that provider + model for all its later turns too, over the agent's default, until an agent calls wick_session_model action=clear.",
	}
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
		// Attach the workflow's author, because a session with no owner mints
		// no per-user credential: the spawn falls back to wick's internal
		// principal, which carries no access tags. That is why an agent node
		// has always looked like it had "no MCP" — not a property of the node,
		// but of a session nobody was attached to.
		//
		// First-writer-wins inside EnsureSessionOwner, so a workflow pinned to
		// a session someone already owns never takes it over. An unattributed
		// workflow (CreatedBy empty) stays ownerless rather than being guessed
		// at — the fallback is narrower access, never wider.
		e.Pool.EnsureSessionOwner(ctx, sessionID, rc.Workflow.CreatedBy)
		// provider (+ model) pins the session there for every turn, not
		// just this run's: the agent's default stops moving it until an
		// agent clears the pin (wick_session_model action=clear).
		if key, model := sessionInitPin(n); key != "" {
			if _, err := e.Pool.PinSession(ctx, sessionID, key, model); err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("pin session provider: %w", err)
			}
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

// sessionInitPin is the provider key and model a session_init node pins
// its session to; "" = no pin. Provider may carry the editor's
// "type/name::model" form, as an agent node's does.
func sessionInitPin(n workflow.Node) (key, model string) {
	key, model = strings.TrimSpace(n.Provider), strings.TrimSpace(n.Model)
	if i := strings.Index(key, "::"); i >= 0 {
		if model == "" {
			model = strings.TrimSpace(key[i+2:])
		}
		key = strings.TrimSpace(key[:i])
	}
	return key, model
}

// resolveSessionInitID picks the sessionID per node config. SessionID
// (template) wins over Preset when both are set.
func resolveSessionInitID(n workflow.Node, rc *workflow.RunContext) (string, error) {
	if n.SessionID != "" {
		return n.SessionID, nil
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
