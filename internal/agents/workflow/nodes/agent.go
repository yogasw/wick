package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/agents/workflow/template"
	"github.com/yogasw/wick/pkg/wickdocs"
)

type agentSchema struct {
	PromptFile string `wick:"required;key=prompt_file;desc=Path to prompt markdown file relative to workflow root (e.g. nodes/summarize.md). Rendered as Go template."`
	Provider   string `wick:"key=provider;desc=Provider name"`
	Skills     string `wick:"key=skills;desc=YAML list of skill names to expose"`
	Tools      string `wick:"key=tools;desc=YAML list of tool names to allowlist"`
	MaxTurns   int    `wick:"key=max_turns;desc=Max agent turns (default unlimited)"`
	Session    string `wick:"key=session;desc=new=fresh session per run, empty=inherit run session"`
}

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

// Dependencies surfaces provider name + each declared skill so
// workflow_describe shows the impact surface of the agent node.
func (e *AgentExecutor) Dependencies(n workflow.Node) []engine.NodeDependency {
	var out []engine.NodeDependency
	if n.Provider != "" {
		out = append(out, engine.NodeDependency{Kind: engine.DepKindProvider, Ref: n.Provider})
	}
	return out
}

func (e *AgentExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Spawn an AI agent with a prompt file and optional skills.",
		WhenToUse:   "Multi-turn reasoning, summarization, or skill-driven action.",
		Example:     "- id: summarize\n  type: agent\n  provider: claude\n  prompt_file: nodes/summarize.md",
		Schema:      integration.StructSchema(agentSchema{}),
		Output:      map[string]string{"text": "string — last assistant message"},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"text":        "Final assistant message after the agent's last turn. The primary downstream field.",
				"tools_used":  "List of tool names the agent invoked during the turn. Empty for skill-less runs.",
				"skills_used": "List of skill names the agent loaded. Useful for audit / cost attribution.",
				"usage":       "Provider token usage breakdown (input/output/total). Provider-specific shape.",
				"session_id":  "Resolved session ID. Reuse via session_from on a downstream agent to continue the conversation.",
			},
			TemplateableFields: []string{"prompt", "prompt_file"},
			Quirks: []string{
				"prompt_file is read relative to the workflow folder (e.g. nodes/summarize.md). The file body is itself rendered as a Go template with .Event / .Node / .Trigger context.",
				"prompt vs prompt_file: prompt is an inline string, prompt_file points to an external markdown file. Use prompt_file once the prompt grows past a few lines so version control diffs stay readable.",
				"arg_modes.prompt defaults to expression. Set to fixed if you want the inline prompt rendered literally without Go template expansion.",
				"session = \"new\" forces a fresh provider session per run. Omit to inherit the workflow-run session set by an upstream session_init node (or the engine default wf_<id>_run_<runID>).",
				"max_turns 0 (unset) = provider default (typically unlimited). Set explicitly when the agent is meant to do a single bounded reasoning step.",
			},
			PairWith: []string{
				"session_init",
				"classify",
				"branch",
			},
			CommonPitfalls: []string{
				"Don't run an agent node before a Slack open_modal action on the same trigger — Slack's trigger_id expires after 3 seconds, the agent call will burn it. Open a skeleton modal first, then call the agent, then update_modal with the agent's output.",
				"Avoid referencing .Node.<this>.parsed assuming structured output was auto-parsed. The engine surfaces raw text; if the prompt is supposed to return JSON, parse it downstream with {{fromJson .Node.<this>.text}}.",
				"Listing a skill in skills: that the provider hasn't installed errors at run time. Call workflow_skills (optionally filter by provider) first to see what's available.",
			},
			InputSample:  `{"provider":"claude","prompt_file":"nodes/summarize.md","max_turns":4,"session":"new"}`,
			OutputSample: `{"text":"User reported an authentication bug after the latest deploy. Suggesting we roll back the JWT middleware.","tools_used":["Read","Grep"],"skills_used":[],"usage":{"input_tokens":1284,"output_tokens":97,"total_tokens":1381},"session_id":"wf_adhoc_3f9b…"}`,
			Examples: []wickdocs.Example{
				{
					Name: "basic_summary",
					YAML: `- id: summarize
  type: agent
  provider: claude
  prompt_file: nodes/summarize.md
  arg_modes:
    prompt_file: expression`,
				},
				{
					Name: "skill_driven_action",
					YAML: `- id: triage
  type: agent
  provider: claude
  prompt_file: nodes/triage.md
  skills:
    - github-issues
  max_turns: 4
  session: new`,
				},
				{
					Name: "continue_session",
					YAML: `- id: follow_up
  type: agent
  provider: claude
  prompt_file: nodes/follow_up.md
  session_from: summarize`,
				},
			},
		},
	}
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
	prompt := n.Prompt
	if n.ArgModes["prompt"] != "fixed" {
		rendered, err := template.Render(n.Prompt, rc.RenderCtx())
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		prompt = rendered
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
