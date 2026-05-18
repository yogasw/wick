// Package mcp bundles every MCP operation the workflow surface
// exposes. Wire each method into the existing internal/mcp dispatch
// layer — transport-agnostic (stdio or HTTP). See workflow-design §9
// for the catalog.
package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/canvas"
	"github.com/yogasw/wick/internal/agents/workflow/channel"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/dataset"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/agents/workflow/scaffold"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/state"
	"github.com/yogasw/wick/internal/agents/workflow/trigger"
)

// Ops bundles every MCP operation surface.
type Ops struct {
	Service     service.Service
	Engine      *engine.Engine
	Router      *trigger.Router
	Canvas      *canvas.Canvas
	Channels    *channel.Registry
	Connectors  *connector.Registry
	Providers   *provider.Registry
	Datasets    dataset.Service
	StateStore  state.Store
	Integration *integration.Registry
}

// New wires the dispatcher.
func New(svc service.Service, e *engine.Engine, router *trigger.Router, c *canvas.Canvas, channels *channel.Registry, connectors *connector.Registry, providers *provider.Registry, datasets dataset.Service, ss state.Store) *Ops {
	return &Ops{
		Service:    svc,
		Engine:     e,
		Router:     router,
		Canvas:     c,
		Channels:   channels,
		Connectors: connectors,
		Providers:  providers,
		Datasets:   datasets,
		StateStore: ss,
	}
}

// WithIntegration wires the integration registry so workflow_integration
// can expose per-channel event + action descriptors (incl. MatchSchema)
// independent of the live Channel registry. Useful for stdio MCP where
// no Slack channel runs but AI still needs full filter schemas.
func (m *Ops) WithIntegration(reg *integration.Registry) *Ops {
	m.Integration = reg
	return m
}

// IntegrationEvents returns every registered event descriptor across all
// channels. Includes MatchSchema + PayloadType for full filter discovery.
func (m *Ops) IntegrationEvents() []integration.EventDescriptor {
	if m.Integration == nil {
		return nil
	}
	return m.Integration.Events()
}

// IntegrationActions returns every registered action descriptor.
func (m *Ops) IntegrationActions() []integration.ActionDescriptor {
	if m.Integration == nil {
		return nil
	}
	return m.Integration.Actions()
}

// ── Tier 1: introspection ───────────────────────────────────────────

// Workspace returns the entry-point response for `workflow_workspace`.
func (m *Ops) Workspace() map[string]any {
	return map[string]any{
		"base_dir":      m.Service.BaseDir(),
		"node_types":    NodeTypesCatalog(),
		"trigger_types": TriggerTypesCatalog(),
		"templates":     []string{"empty", "support-triage", "incident-response", "daily-digest"},
	}
}

// NodeTypes returns the catalog used by `workflow_node_types`.
func (m *Ops) NodeTypes() []NodeTypeInfo { return NodeTypesCatalog() }

// TriggerTypes returns the catalog used by `workflow_trigger_types`.
func (m *Ops) TriggerTypes() []TriggerTypeInfo { return TriggerTypesCatalog() }

// ChannelsList returns the channel registry introspection rows.
func (m *Ops) ChannelsList() []channel.Info {
	if m.Channels == nil {
		return nil
	}
	return m.Channels.Describe()
}

// ConnectorsList returns the connector registry introspection rows.
func (m *Ops) ConnectorsList() []connector.Info {
	if m.Connectors == nil {
		return nil
	}
	return m.Connectors.Describe()
}

// ProvidersList returns the provider registry introspection rows.
func (m *Ops) ProvidersList() []provider.Info {
	if m.Providers == nil {
		return nil
	}
	return m.Providers.Describe()
}

// SkillsList returns the catalog from one or all providers.
func (m *Ops) SkillsList(ctx context.Context, providerName string) ([]provider.Skill, error) {
	if m.Providers == nil {
		return nil, fmt.Errorf("no provider registry")
	}
	if providerName != "" {
		p, err := m.Providers.Get(providerName)
		if err != nil {
			return nil, err
		}
		return p.ListSkills(ctx)
	}
	out := []provider.Skill{}
	for _, name := range m.Providers.List() {
		p, _ := m.Providers.Get(name)
		s, err := p.ListSkills(ctx)
		if err != nil {
			continue
		}
		out = append(out, s...)
	}
	return out, nil
}

// List returns workflow IDs + metadata.
func (m *Ops) List() ([]Summary, error) {
	ids, err := m.Service.List()
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, id := range ids {
		w, err := m.Service.Load(id)
		if err != nil {
			continue
		}
		out = append(out, Summary{
			ID:      w.ID,
			Name:    w.Name,
			Enabled: w.Enabled,
			Version: w.Version,
		})
	}
	return out, nil
}

// Get returns the full workflow.
func (m *Ops) Get(id string) (workflow.Workflow, error) { return m.Service.Load(id) }

// ListFiles returns relative file paths in the workflow folder.
func (m *Ops) ListFiles(id string) ([]string, error) { return m.Service.ListFiles(id) }

// ReadFile returns the content of one file.
func (m *Ops) ReadFile(id, path string) ([]byte, error) { return m.Service.ReadFile(id, path) }

// ── Tier 2: write ────────────────────────────────────────────────────

// CreateInput is the payload for `workflow_create`.
//
// ID is the on-disk folder name. Optional — when empty, Create
// generates a UUID so renaming the display name later doesn't break
// run history, indexed logs, or shared edit URLs. Power users (MCP,
// CLI, tests) may pin an explicit id for human-readable folders.
type CreateInput struct {
	ID       string `json:"id,omitempty"`
	Template string `json:"template,omitempty"`
	Name     string `json:"name,omitempty"`
}

// Create scaffolds a new workflow from a template.
func (m *Ops) Create(in CreateInput) (workflow.Workflow, error) {
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	if err := parse.ValidateID(id); err != nil {
		return workflow.Workflow{}, err
	}
	w := scaffold.Workflow(id, in.Name, in.Template)
	if err := m.Service.Create(id, w, nil); err != nil {
		return workflow.Workflow{}, err
	}
	return m.Service.Load(id)
}

// WriteFile atomically writes a file inside the workflow folder.
func (m *Ops) WriteFile(id, path string, data []byte) error {
	return m.Service.WriteFile(id, path, data)
}

// DeleteFile removes a file inside the workflow folder.
func (m *Ops) DeleteFile(id, path string) error { return m.Service.DeleteFile(id, path) }

// Delete removes the workflow folder + unregisters scheduling.
func (m *Ops) Delete(id string) error {
	if m.Router != nil {
		m.Router.Unregister(id)
	}
	return m.Service.Delete(id)
}

// AddNode wraps Canvas.AddNode.
func (m *Ops) AddNode(id string, n workflow.Node) (workflow.Workflow, error) {
	return m.Canvas.AddNode(id, n)
}

// UpdateNode wraps Canvas.UpdateNode.
func (m *Ops) UpdateNode(id, nodeID string, patch map[string]any) (workflow.Workflow, error) {
	return m.Canvas.UpdateNode(id, nodeID, patch)
}

// DeleteNode wraps Canvas.DeleteNode.
func (m *Ops) DeleteNode(id, nodeID string) (workflow.Workflow, error) {
	return m.Canvas.DeleteNode(id, nodeID)
}

// Connect wraps Canvas.Connect.
func (m *Ops) Connect(id, from, to, caseLabel string) (workflow.Workflow, error) {
	return m.Canvas.Connect(id, from, to, caseLabel)
}

// Disconnect wraps Canvas.Disconnect.
func (m *Ops) Disconnect(id, from, to string) (workflow.Workflow, error) {
	return m.Canvas.Disconnect(id, from, to)
}

// MoveNode wraps Canvas.MoveNode.
func (m *Ops) MoveNode(id, nodeID string, x, y int) (workflow.Workflow, error) {
	return m.Canvas.MoveNode(id, nodeID, x, y)
}

// SetTriggers wraps Canvas.SetTriggers.
func (m *Ops) SetTriggers(id string, triggers []workflow.Trigger) (workflow.Workflow, error) {
	return m.Canvas.SetTriggers(id, triggers)
}

// Toggle wraps Canvas.Toggle.
func (m *Ops) Toggle(id string, enabled bool) (workflow.Workflow, error) {
	return m.Canvas.Toggle(id, enabled)
}

// ── Tier 3: action ───────────────────────────────────────────────────

// ValidateResult is the response for `workflow_validate`.
type ValidateResult struct {
	OK       bool          `json:"ok"`
	Errors   []parse.Error `json:"errors,omitempty"`
	Warnings []parse.Error `json:"warnings,omitempty"`
}

// Validate runs parse + validate (no guard).
func (m *Ops) Validate(id string) ValidateResult {
	w, err := m.Service.Load(id)
	if err != nil {
		return ValidateResult{OK: false, Errors: []parse.Error{{Path: "load", Message: err.Error()}}}
	}
	r := parse.Validate(w)
	return ValidateResult{OK: r.Ok(), Errors: r.Errors, Warnings: r.Warnings}
}

// Simulate dry-runs a workflow with a synthetic event.
func (m *Ops) Simulate(ctx context.Context, id string, evt workflow.Event) (workflow.RunState, error) {
	w, err := m.Service.Load(id)
	if err != nil {
		return workflow.RunState{}, err
	}
	return m.Engine.Run(ctx, w, evt)
}

// RunNow enqueues a manual run for one explicit id. Bypasses
// Enabled + trigger-match checks so admins can fire a disabled
// workflow from the UI Run-Now button. Compare with Router.Dispatch
// which is the trigger-source path.
func (m *Ops) RunNow(ctx context.Context, id string, evt workflow.Event) error {
	return m.RunNowWith(ctx, id, nil, evt)
}

// RunNowWith fires a single run with an explicit Workflow override.
// The UI uses this so Run Now executes the freshly-saved DRAFT
// (workflow.draft.yaml) without waiting for Publish — router's
// registered copy stays on the published version so cron / channel
// / webhook triggers keep firing live.
func (m *Ops) RunNowWith(ctx context.Context, id string, w *workflow.Workflow, evt workflow.Event) error {
	if m.Router == nil {
		return fmt.Errorf("router not configured")
	}
	if evt.Type == "" {
		evt.Type = string(workflow.TriggerManual)
	}
	return m.Router.RunNowWith(ctx, id, w, evt)
}

// GetRuns returns recent run IDs for an id.
func (m *Ops) GetRuns(id string, limit int) ([]string, error) {
	if m.StateStore == nil {
		return nil, nil
	}
	runs, err := m.StateStore.ListRuns(id)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

// RunSummary is the lightweight row the editor's Runs panel shows —
// ID + started timestamp + status. Loaded eagerly because the panel
// only displays the most recent N runs (default 20) and one
// state.json read per run is cheap.
type RunSummary struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// GetRunSummaries returns one page of recent runs, newest first.
// Reads from the sharded index (`runs/index/<date>-<seq>.jsonl`)
// instead of scanning the per-run subdirs, so the cost stays
// constant whether the workflow has 10 or 100,000 historical runs.
// hasMore=true when older pages exist.
func (m *Ops) GetRunSummaries(id string, page, pageSize int) ([]RunSummary, bool, error) {
	entries, hasMore, err := m.StateStore.IndexList(id, page, pageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]RunSummary, 0, len(entries))
	for _, e := range entries {
		out = append(out, RunSummary{
			ID:        e.ID,
			Status:    e.Status,
			StartedAt: e.StartedAt,
			EndedAt:   e.EndedAt,
		})
	}
	return out, hasMore, nil
}

// Summary is the row shape for `workflow_list`.
type Summary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Version int    `json:"version"`
}

// NodeTypeInfo is one row of the node-type catalog.
type NodeTypeInfo struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Example     string         `json:"example,omitempty"`
	WhenToUse   string         `json:"when_to_use"`
}

// TriggerTypeInfo is one row of the trigger-type catalog.
type TriggerTypeInfo struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Example     string         `json:"example,omitempty"`
}

// NodeTypesCatalog returns the AI-introspectable node type metadata.
func NodeTypesCatalog() []NodeTypeInfo {
	return []NodeTypeInfo{
		{Type: "classify", Description: "Classify natural-language input into an enum via LLM. Returns verdict + confidence + reasoning. Route via case: labels.", WhenToUse: "Input is free text and needs to be bucketed into a small set of cases."},
		{Type: "agent", Description: "Spawn an AI agent with prompt, optional skills, and tool allowlist. Returns last assistant text.", WhenToUse: "Multi-turn reasoning or skill-driven action."},
		{Type: "channel", Description: "Invoke a channel module action (send_message, reply_thread, open_modal, ...).", WhenToUse: "Send messages out via Slack/Telegram/REST."},
		{Type: "connector", Description: "Invoke a connector module operation. Reuses MCP audit, encrypted fields, destructive flag.", WhenToUse: "Call any registered external integration."},
		{Type: "shell", Description: "Execute a local shell command. Captures stdout/stderr/exit_code.", WhenToUse: "Operating on local files or running a tool only available as a CLI."},
		{Type: "http", Description: "Make an HTTP request. Supports retry policy, template-rendered URL/headers/query/body.", WhenToUse: "Direct external API calls outside a connector module."},
		{Type: "db_query", Description: "Run a parameterized SQL query against a configured DSN.", WhenToUse: "Reading from an external user database."},
		{Type: "transform", Description: "Pure-function transform via gotemplate / jsonpath / jq.", WhenToUse: "Reshape an upstream output for downstream consumption."},
		{Type: "branch", Description: "Deterministic if/switch routing via Go template expression. Filters edges by case: label.", WhenToUse: "Routing logic is structured (no natural language)."},
		{Type: "parallel", Description: "Fan out to N named branches; wait per on_failure policy.", WhenToUse: "Independent sub-flows that can run concurrently."},
		{Type: "merge", Description: "Fan-in wait-for-all; composes outputs per strategy (object|array|first|last).", WhenToUse: "Diamond topology requiring all parents complete."},
		{Type: "end", Description: "Terminator. Captures a final result template for {{.Run.final_result}}.", WhenToUse: "Explicit end-of-flow with a result payload."},
		{Type: "dataset_get", Description: "Load one row by primary key. Branches on found/not_found.", WhenToUse: "Lookup a state row before deciding next action."},
		{Type: "dataset_exists", Description: "Check whether any row matches. Branches on true/false.", WhenToUse: "Dedup webhook events or guard against duplicate work."},
		{Type: "dataset_query", Description: "Multi-row search with where/order_by/limit.", WhenToUse: "List or paginate stored rows."},
		{Type: "dataset_count", Description: "Count rows matching where without loading them.", WhenToUse: "Cheap statistic for decisions."},
		{Type: "dataset_insert", Description: "Insert a new row; fails on PK conflict.", WhenToUse: "Idempotency-by-PK guard plus persistence."},
		{Type: "dataset_upsert", Description: "Insert or update by primary key. Returns action: insert|update.", WhenToUse: "Idempotent record sync."},
		{Type: "dataset_delete", Description: "Delete rows matching where.", WhenToUse: "Cleanup expired state."},
	}
}

// TriggerTypesCatalog returns the trigger-type metadata.
func TriggerTypesCatalog() []TriggerTypeInfo {
	return []TriggerTypeInfo{
		{Type: "cron", Description: "Run on a cron schedule.", Example: `{type: cron, schedule: "0 8 * * *", timezone: UTC}`},
		{Type: "channel", Description: "Inbound channel event (message, action, submission, ...).", Example: `{type: channel, channel: slack, event: message, target: "#inbox"}`},
		{Type: "webhook", Description: "External HTTP POST to /hooks/<path>. HMAC SHA-256 verifiable.", Example: `{type: webhook, path: /hooks/orders/{id}, secret_ref: wick_enc_...}`},
		{Type: "manual", Description: "Admin UI button or MCP workflow_run_now.", Example: `{type: manual, label: "Run digest now"}`},
		{Type: "schedule_at", Description: "One-shot fire at a future timestamp.", Example: `{type: schedule_at, at: 2026-06-01T08:00:00Z}`},
		{Type: "error", Description: "Fire on failure of another workflow. Filters by source_workflow/severity/node_types.", Example: `{type: error, source_workflow: "*", severity: [high, critical]}`},
	}
}
