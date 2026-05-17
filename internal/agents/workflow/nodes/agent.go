package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// AgentEvent is the minimal event shape the agent executor consumes
// while waiting for a turn to complete. Defined here (not imported
// from tools/agents) so the workflow package stays free of a cycle on
// the UI broadcaster.
//
// Type values mirror agents/event.EventType.String() output:
// "text_delta", "tool_use", "tool_result", "done", "error", "thinking",
// "session_start". Anything else is ignored by the executor.
type AgentEvent struct {
	Type string
	Data string
}

// AgentSubscribeFn returns a receive channel of AgentEvents for one
// sessionID plus an unsub function. The executor subscribes before
// dispatching the pool send so no leading event is lost. Setup wires a
// concrete adapter around tools/agents.Broadcaster.
type AgentSubscribeFn func(sessionID string) (<-chan AgentEvent, func())

// AgentExecutor invokes a provider's AgentCall for a `type: agent`
// node. When Pool + Subscribe are wired and the resolved provider can
// route via pool, the executor enqueues through the agent pool (queue
// FIFO, session reuse, sidebar visibility); otherwise it falls back to
// the non-pool provider path for codex/gemini.
type AgentExecutor struct {
	Providers *provider.Registry
	Pool      *pool.Pool
	Subscribe AgentSubscribeFn
}

// NewAgentExecutor wires the executor. Pool + Subscribe may be nil for
// non-claude-only test setups; in that case all agent calls go through
// provider.AgentCall (the non-pool path).
func NewAgentExecutor(reg *provider.Registry, p *pool.Pool, sub AgentSubscribeFn) *AgentExecutor {
	return &AgentExecutor{Providers: reg, Pool: p, Subscribe: sub}
}

// Execute runs the agent node. Routes via pool when configured.
func (e *AgentExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Providers == nil {
		return workflow.NodeOutput{}, fmt.Errorf("agent: no provider registry configured")
	}
	prov, err := e.Providers.Get(n.Provider)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	prompt, err := template.Render(n.Prompt, rc.RenderCtx())
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	if err := validateSkills(ctx, prov, n.Skills); err != nil {
		return workflow.NodeOutput{}, err
	}

	sessionID, err := resolveAgentSessionID(n, rc)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	if rc.AgentSessionIDs == nil {
		rc.AgentSessionIDs = map[string]string{}
	}
	rc.AgentSessionIDs[n.ID] = sessionID

	// Pool path — only for providers wired through the agent pool
	// (claude today; codex/gemini stay on the cliProvider path until
	// pool gains multi-factory support).
	if e.Pool != nil && e.Subscribe != nil && providerUsesPool(prov.Name()) {
		return e.runViaPool(ctx, n, prompt, sessionID)
	}

	req := provider.AgentRequest{
		Prompt:    prompt,
		Preset:    n.Preset,
		Workspace: n.Workspace,
		Skills:    n.Skills,
		Tools:     n.Tools,
		MaxTurns:  n.MaxTurns,
		SessionID: sessionID,
	}
	res, err := prov.AgentCall(ctx, req)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("agent call: %w", err)
	}
	return workflow.NodeOutput{
		Result: res.Text,
		Fields: map[string]any{
			"text":        res.Text,
			"tools_used":  res.ToolsUsed,
			"skills_used": res.SkillsUsed,
			"usage":       res.Usage,
			"session_id":  sessionID,
		},
	}, nil
}

// runViaPool enqueues the agent prompt through the agent pool and
// awaits a turn-complete event. The pool handles queue FIFO,
// MaxConcurrent caps, idle preemption, and sidebar visibility.
//
// Subscription happens before the Send so the first text_delta can't
// race past the receiver. The loop exits on Done (success), Error
// (failed turn), or ctx cancellation (timeout / workflow abort).
func (e *AgentExecutor) runViaPool(ctx context.Context, n workflow.Node, prompt, sessionID string) (workflow.NodeOutput, error) {
	evCh, unsub := e.Subscribe(sessionID)
	defer unsub()

	if err := e.Pool.SendWithWorkspace(ctx, sessionID, "default", "workflow", "user", prompt, n.Workspace); err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("pool send: %w", err)
	}

	var textBuf strings.Builder
	toolsUsed := []string{}
	for {
		select {
		case <-ctx.Done():
			return workflow.NodeOutput{}, ctx.Err()
		case ev, ok := <-evCh:
			if !ok {
				return workflow.NodeOutput{}, fmt.Errorf("event channel closed before turn completed")
			}
			switch ev.Type {
			case "text_delta":
				textBuf.WriteString(ev.Data)
			case "tool_use":
				if ev.Data != "" {
					toolsUsed = append(toolsUsed, ev.Data)
				}
			case "error":
				return workflow.NodeOutput{}, fmt.Errorf("agent error: %s", ev.Data)
			case "done":
				text := strings.TrimSpace(textBuf.String())
				return workflow.NodeOutput{
					Result: text,
					Fields: map[string]any{
						"text":       text,
						"tools_used": toolsUsed,
						"session_id": sessionID,
					},
				}, nil
			}
		}
	}
}

// resolveAgentSessionID picks the sessionID per the override order
// documented in pool.md:
//
//  1. session_from set    → reuse the sessionID resolved for that node
//  2. session == "new"    → fresh UUID per call
//  3. rc.DefaultAgentSessionID → set by an upstream session_init node
//  4. fallback            → "wf:<id>:run:<runID>"
func resolveAgentSessionID(n workflow.Node, rc *workflow.RunContext) (string, error) {
	if n.SessionFrom != "" {
		id, ok := rc.AgentSessionIDs[n.SessionFrom]
		if !ok {
			return "", fmt.Errorf("session_from %q: upstream node not yet executed (forward ref or missing session_init/agent)", n.SessionFrom)
		}
		return id, nil
	}
	if n.Session == workflow.SessionNew {
		return "wf_adhoc_" + uuid.NewString(), nil
	}
	if rc.DefaultAgentSessionID != "" {
		return rc.DefaultAgentSessionID, nil
	}
	return DefaultRunSessionID(rc.Workflow.ID, rc.RunID), nil
}

// DefaultRunSessionID is the engine fallback when neither a
// session_init node nor a per-node override is set. Format is
// "wf_<id>_run_<runID>" — underscores keep the string inside the
// sessionID charset (no colon; the storage validator at
// internal/agents/storage/validate.go limits the alphabet to
// `[A-Za-z0-9._-]`).
func DefaultRunSessionID(id, runID string) string {
	return fmt.Sprintf("wf_%s_run_%s", id, runID)
}

// providerUsesPool reports whether a provider name routes through the
// shared agent pool. Today only claude has a pool factory; codex and
// gemini stay on the cliProvider one-shot path.
func providerUsesPool(name string) bool {
	return strings.EqualFold(name, "claude")
}

func validateSkills(ctx context.Context, prov provider.Provider, skills []string) error {
	if len(skills) == 0 {
		return nil
	}
	have, err := prov.ListSkills(ctx)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, s := range have {
		set[s.Name] = true
	}
	for _, want := range skills {
		if !set[want] {
			return fmt.Errorf("agent skill %q not available on provider %q", want, prov.Name())
		}
	}
	return nil
}
