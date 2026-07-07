// Package workflow is the domain for AI-orchestrated multi-step
// automations stored at `<BaseDir>/workflows/<id>/`. A workflow is a
// directed acyclic graph of typed nodes (classify/agent/connector/
// shell/http/branch/parallel/merge/datatable_*/transform/end) with one
// or more triggers (cron, channel, webhook, manual, schedule_at,
// error). The engine walks the graph node-by-node, persists state per
// run, and reuses existing wick infra (channels, connectors, providers,
// pool).
//
// See `internal/planning/archive/workflow-design.md` for the full contract.
package workflow

import (
	"time"
)

// NodeType is the discriminator for the polymorphic Node body.
type NodeType string

const (
	NodeClassify        NodeType = "classify"
	NodeAgent           NodeType = "agent"
	NodeChannel         NodeType = "channel"
	NodeConnector       NodeType = "connector"
	NodeShell           NodeType = "shell"
	NodeSwitch          NodeType = "switch"
	NodeGoScript        NodeType = "go_script"
	NodePython          NodeType = "python"
	NodeHTTP            NodeType = "http"
	NodeDBQuery         NodeType = "db_query"
	NodeTransform       NodeType = "transform"
	NodeBranch          NodeType = "branch"
	NodeParallel        NodeType = "parallel"
	NodeMerge           NodeType = "merge"
	NodeEnd             NodeType = "end"
	NodeDataTableGet    NodeType = "datatable_get"
	NodeDataTableExists NodeType = "datatable_exists"
	NodeDataTableQuery  NodeType = "datatable_query"
	NodeDataTableInsert NodeType = "datatable_insert"
	NodeDataTableUpsert NodeType = "datatable_upsert"
	NodeDataTableDelete NodeType = "datatable_delete"
	NodeDataTableCount  NodeType = "datatable_count"
	NodeSessionInit     NodeType = "session_init"
	NodeWebhookRespond  NodeType = "webhook_respond"
)

// IsDataTableNode reports whether t is one of the datatable_* variants.
func (t NodeType) IsDataTableNode() bool {
	switch t {
	case NodeDataTableGet, NodeDataTableExists, NodeDataTableQuery,
		NodeDataTableInsert, NodeDataTableUpsert, NodeDataTableDelete, NodeDataTableCount:
		return true
	}
	return false
}

// IsBranchSource reports whether nodes of this type produce a verdict
// that filters outgoing edges by `case:`.
//
// datatable_exists and datatable_get emit verdicts ("true"/"false" and
// "found"/"not_found") that engines + canvas treat as case keys so a
// single node can branch dedup / lookup flows without a separate branch
// node — matches the n8n Data Table "If exists" handler pattern.
func (t NodeType) IsBranchSource() bool {
	return t == NodeClassify || t == NodeBranch || t == NodeSwitch ||
		t == NodeDataTableExists || t == NodeDataTableGet
}

// TriggerType discriminator for the polymorphic Trigger body.
type TriggerType string

const (
	TriggerCron       TriggerType = "cron"
	TriggerChannel    TriggerType = "channel"
	TriggerWebhook    TriggerType = "webhook"
	TriggerManual     TriggerType = "manual"
	TriggerScheduleAt TriggerType = "schedule_at"
	TriggerError      TriggerType = "error"
)

// Workflow is the root document for a workflow definition. Field tags
// carry json so the struct serialises correctly for the DB store and
// the SPA JSON API. Keep the two in sync — drift silently produces
// JSON keys with capitalised field names because Go defaults to that
// without a json tag.
//
// ID is the stable folder name (UUID for canvas-created workflows,
// arbitrary id for legacy hand-edited ones). Display title lives in
// Name and is freely renameable — the folder/URL/log paths stay
// anchored to ID so run history survives a rename.
type Workflow struct {
	ID             string             `json:"id"`
	Version        int                `json:"version"`
	Name           string             `json:"name"`
	Description    string             `json:"description,omitempty"`
	Enabled        bool               `json:"enabled"`
	MaxDurationSec int                `json:"max_duration_sec,omitempty"`
	Triggers       []Trigger          `json:"triggers"`
	Queue          QueuePolicy        `json:"queue,omitempty"`
	Concurrency    ConcurrencyPolicy  `json:"concurrency,omitempty"`
	Env            []EnvField         `json:"env,omitempty"`
	DataTables     []DataTableBinding `json:"data_tables,omitempty"`
	Graph          Graph              `json:"graph"`
	OnError        *OnErrorBinding    `json:"on_error,omitempty"`
	CreatedBy      string             `json:"created_by,omitempty"`
	CreatedAt      time.Time          `json:"created_at,omitempty"`
	Canvas         map[string]any     `json:"_canvas,omitempty"`
}

// QueuePolicy controls per-workflow concurrency.
type QueuePolicy struct {
	MaxSize    int    `json:"max_size,omitempty"`
	OnOverflow string `json:"on_overflow,omitempty"`
}

// ConcurrencyPolicy controls how many runs of this workflow may execute
// simultaneously. When Enabled is false (default) runs are serialised —
// the single worker drains the queue one item at a time, preserving
// event order. When Enabled is true, up to Max goroutines may execute
// runs concurrently. Max=0 means "use default" (2); set a positive value
// to override. Both bounds are further capped by the global router cap.
type ConcurrencyPolicy struct {
	Enabled bool `json:"enabled"`
	Max     int  `json:"max,omitempty"` // 0 = use default (2); >0 = explicit cap
}

// Overflow policy values.
const (
	OverflowDropOldest = "drop_oldest"
	OverflowDropNew    = "drop_new"
	OverflowReject     = "reject"
)

// Graph is the DAG body: flat node list + separate edge list.
type Graph struct {
	Entry string `json:"entry"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Edge is a directed connection from one node to another. Case is
// only meaningful when From is a classify or branch node.
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Case  string `json:"case,omitempty"`
	Label string `json:"label,omitempty"`
}

// Node is a single step in the graph. Fields are a flat union — only
// the subset relevant to Type is read by the executor. Validator
// rejects nodes that set fields outside their type.
type Node struct {
	// Common
	ID           string         `json:"id"`
	Type         NodeType       `json:"type"`
	Label        string         `json:"label,omitempty"`
	Description  string         `json:"description,omitempty"`
	TimeoutSec   int            `json:"timeout_sec,omitempty"`
	Retry        *RetryPolicy   `json:"retry,omitempty"`
	OnFailure    string         `json:"on_failure,omitempty"`
	Fallback     string         `json:"fallback,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`

	// parallel
	Branches []string `json:"branches,omitempty"`

	// merge
	Inputs   []string `json:"inputs,omitempty"`
	Strategy string   `json:"strategy,omitempty"`

	// classify + agent
	Provider string `json:"provider,omitempty"`
	Preset   string `json:"preset,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Session  string `json:"session,omitempty"`

	// agent override — copy resolved sessionID from another node in
	// this run. Must reference an upstream agent or session_init node.
	SessionFrom string `json:"session_from,omitempty"`

	// session_init — preset shortcut OR rendered template id. Mutually
	// exclusive; SessionID wins when both set. `Preset` reuses the
	// classify/agent Preset field above.
	SessionID string `json:"session_id,omitempty"`

	// classify
	OutputCases         []string          `json:"output_cases,omitempty"`
	StructuredOutput    *bool             `json:"structured_output,omitempty"`
	Normalize           *bool             `json:"normalize,omitempty"`
	FuzzyMatch          bool              `json:"fuzzy_match,omitempty"`
	RetryOnMismatch     int               `json:"retry_on_mismatch,omitempty"`
	ConfidenceThreshold float64           `json:"confidence_threshold,omitempty"`
	Examples            []ClassifyExample `json:"examples,omitempty"`

	// agent
	Workspace         string   `json:"workspace,omitempty"`
	Skills            []string `json:"skills,omitempty"`
	Tools             []string `json:"tools,omitempty"`
	MaxTurns          int      `json:"max_turns,omitempty"`
	Thinking          string   `json:"thinking,omitempty"`
	MaxThinkingTokens int      `json:"max_thinking_tokens,omitempty"`
	RequireStatus     bool     `json:"require_status,omitempty"`

	// channel (action) — Channel field name avoided clash with Event.Channel
	ChannelName string            `json:"channel,omitempty"`
	Op          string            `json:"op,omitempty"`
	Args        map[string]any    `json:"args,omitempty"`
	ArgModes    map[string]string `json:"arg_modes,omitempty"`

	// connector — uses row_id for instance (datatable_* nodes own `row:`)
	Module string `json:"module,omitempty"`
	Row    string `json:"row_id,omitempty"`
	// Account pins the node to a connected SSO account of the instance;
	// the creds resolver injects that account's token (user_token). Empty
	// = non-SSO or row-level creds. Access is enforced at the instance
	// (tag) level; account only selects which token to use.
	Account string `json:"account_id,omitempty"`

	// shell
	Command     []string          `json:"command,omitempty"`
	ShellEnv    map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	ParseOutput string            `json:"parse_output,omitempty"`

	// http
	Method        string            `json:"method,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Query         map[string]string `json:"query,omitempty"`
	Body          string            `json:"body,omitempty"`
	ParseResponse string            `json:"parse_response,omitempty"`

	// db_query — uses `sql:` key (HTTP node already owns `query:` for query params)
	Database string   `json:"database,omitempty"`
	SQL      string   `json:"sql,omitempty"`
	SQLArgs  []string `json:"sql_args,omitempty"`

	// transform
	Engine     string `json:"engine,omitempty"`
	Input      string `json:"input,omitempty"`
	Expression string `json:"expression,omitempty"`

	// go_script — full Go program. Engine pipes RenderCtx JSON to
	// stdin, parses stdout as JSON for the result.
	Code string `json:"code,omitempty"`

	// branch
	Expr string `json:"expr,omitempty"`

	// switch — first-match-wins rule list. Each rule's `when` is a Go
	// template that renders to a bool (supports the same binary ops as
	// `branch`: ==, !=, <, <=, >, >=) or any non-empty string (truthy).
	// First rule whose `when` evaluates true wins; engine emits
	// Verdict=<rule.case> so the edge `case: <label>` filter routes
	// downstream. DefaultCase fires when no rule matches.
	Cases       []SwitchCase `json:"cases,omitempty"`
	DefaultCase string       `json:"default_case,omitempty"`

	// datatable_*
	Table          string              `json:"table,omitempty"`
	Where          map[string]any      `json:"where,omitempty"`
	Conditions     []DataTableCondYAML `json:"conditions,omitempty"`
	ConditionModes map[string]string   `json:"condition_modes,omitempty"`
	RowModes       map[string]string   `json:"row_modes,omitempty"`
	Key            map[string]any      `json:"key,omitempty"`
	RowValues      map[string]any      `json:"row,omitempty"`
	OrderBy        []DataTableOrder    `json:"order_by,omitempty"`
	Limit          int                 `json:"limit,omitempty"`
	Offset         int                 `json:"offset,omitempty"`

	// end
	Result string `json:"result,omitempty"`

	// webhook_respond — sends a custom HTTP response back to the webhook caller.
	// Requires the trigger's respond_mode = "respond_node".
	RespondStatus  int               `json:"respond_status,omitempty"`
	RespondBody    string            `json:"respond_body,omitempty"`
	RespondHeaders map[string]string `json:"respond_headers,omitempty"`
}

// OnFailure values.
const (
	FailHalt     = "halt"
	FailSkip     = "skip"
	FailFallback = "fallback"
)

// MergeStrategy values.
const (
	MergeObject = "object"
	MergeArray  = "array"
	MergeFirst  = "first"
	MergeLast   = "last"
)

// Session modes for agent/classify nodes.
//
// Legacy values "root" and "persistent" predate the pool-integration
// design; they remain in the constant list so loaders that touch old
// YAML round-trip cleanly, but the engine treats them as equivalent to
// the per-run default (no override). New workflows should use the
// `session_init` node instead — see internal/planning/archive/workflow/pool.md.
const (
	SessionNew        = "new"
	SessionRoot       = "root"
	SessionPersistent = "persistent"
)

// Session preset values for the `session_init` node. `preset:` and `id:`
// are mutually exclusive — when `id:` is set the executor renders it as
// a template; otherwise it falls back to the preset pattern.
const (
	SessionPresetWorkflowRun    = "workflow_run"
	SessionPresetWorkflowGlobal = "workflow_global"
	SessionPresetNew            = "new"
)

// NodeSession is the per-agent-node session override. Empty struct
// means "use rc.DefaultAgentSessionID (or the engine fallback)".
type NodeSession struct {
	From string `json:"from,omitempty"`
	Mode string `json:"mode,omitempty"`
}

// RetryPolicy on a node.
type RetryPolicy struct {
	Max        int `json:"max"`
	BackoffSec int `json:"backoff_sec,omitempty"`
}

// ClassifyExample is a few-shot prompt example.
type ClassifyExample struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// SwitchCase is one rule row for `switch` nodes.
type SwitchCase struct {
	When string `json:"when"`
	Case string `json:"case"`
}

// DataTableOrder is one order-by clause.
type DataTableOrder struct {
	Column    string `json:"column"`
	Direction string `json:"direction,omitempty"`
}

// DataTableCondYAML is one condition row declared in a workflow.
// Name kept for backward compatibility; storage is JSON.
type DataTableCondYAML struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  any    `json:"value,omitempty"`
}

// EnvField is one entry of the workflow's env schema.
type EnvField struct {
	Name        string            `json:"name"`
	Widget      string            `json:"widget,omitempty"`
	Desc        string            `json:"desc,omitempty"`
	Default     string            `json:"default,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Locked      bool              `json:"locked,omitempty"`
	Hidden      bool              `json:"hidden,omitempty"`
	Options     []EnvOption       `json:"options,omitempty"`
	VisibleWhen map[string]string `json:"visible_when,omitempty"`
}

// IsSecret reports whether this field is the encrypted variant.
func (f EnvField) IsSecret() bool { return f.Widget == "secret" }

// EnvOption is one choice for dropdown/picker widgets.
type EnvOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DataTableBinding wires a workflow-local alias to a data table slug.
type DataTableBinding struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
	Mode string `json:"mode,omitempty"`
}

// Trigger is one polymorphic trigger entry. Fields are a flat union
// like Node — the validator gates each field to its Type.
type Trigger struct {
	ID        string      `json:"id,omitempty"`
	Type      TriggerType `json:"type"`
	EntryNode string      `json:"entry_node,omitempty"`

	// cron
	Schedule string `json:"schedule,omitempty"`
	Timezone string `json:"timezone,omitempty"`

	// channel
	ChannelName  string            `json:"channel,omitempty"`
	Event        string            `json:"event,omitempty"`
	Target       string            `json:"target,omitempty"`
	Match        map[string]any    `json:"match,omitempty"`
	MatchEnabled bool              `json:"match_enabled,omitempty"`
	MatchModes   map[string]string `json:"match_modes,omitempty"`
	Whitelist    *Whitelist        `json:"whitelist,omitempty"`
	DedupTTLSec  int               `json:"dedup_ttl_sec,omitempty"`
	ReplySource  *bool             `json:"reply_source,omitempty"`

	// webhook
	Path      string `json:"path,omitempty"`
	Method    string `json:"method,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
	ParseBody string `json:"parse_body,omitempty"`
	BodyToVar string `json:"body_to_var,omitempty"`
	// RespondMode controls when and how the HTTP response is sent back to
	// the webhook caller. Three values are supported:
	//
	//   "immediately" (default)
	//       202 Accepted is returned as soon as the run is enqueued.
	//       The workflow executes asynchronously; the caller gets no
	//       output. Use for fire-and-forget integrations.
	//
	//   "last_node"
	//       The handler blocks until the workflow completes (or the
	//       30-second timeout expires → 504). On success, the last
	//       completed node's output is serialised as JSON and returned
	//       with HTTP 200. On workflow failure, HTTP 500 is returned.
	//       Use when the caller needs the result synchronously and the
	//       workflow is expected to finish quickly (< 30s).
	//
	//   "respond_node"
	//       The handler blocks like "last_node" but the response body,
	//       status code, and headers are taken from the first
	//       webhook_respond node that completes. If no webhook_respond
	//       node runs within 30s, HTTP 504 is returned.
	//       Use when you need full control over the HTTP response shape.
	//
	// Timeout for both blocking modes: 30s (see trigger.respondTimeout).
	// For workflows that take longer, use "immediately" and poll via the
	// run-status API.
	RespondMode string `json:"respond_mode,omitempty"`

	// manual
	Label       string `json:"label,omitempty"`
	ButtonLabel string `json:"button_label,omitempty"`
	RequireRole string `json:"require_role,omitempty"`

	// schedule_at
	At          time.Time `json:"at,omitempty"`
	DeleteAfter bool      `json:"delete_after,omitempty"`

	// error
	SourceWorkflow string   `json:"source_workflow,omitempty"`
	Severity       []string `json:"severity,omitempty"`
	NodeTypes      []string `json:"node_types,omitempty"`
}

// Whitelist filters who can fire a trigger.
type Whitelist struct {
	Users  []string `json:"users,omitempty"`
	Groups []string `json:"groups,omitempty"`
	IPs    []string `json:"ips,omitempty"`
}

// OnErrorBinding declares which error-handler workflow to fire on failure.
type OnErrorBinding struct {
	TriggerWorkflow   string `json:"trigger_workflow"`
	Severity          string `json:"severity,omitempty"`
	IncludeState      bool   `json:"include_state,omitempty"`
	IncludeNodeOutput bool   `json:"include_node_output,omitempty"`
}

// Event is the trigger payload passed to a run.
type Event struct {
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype,omitempty"`
	Channel   string         `json:"channel,omitempty"`
	At        time.Time      `json:"at"`
	Payload   map[string]any `json:"payload,omitempty"`
	TriggerID string         `json:"trigger_id,omitempty"`
}

// RunStatus values.
// RespondMode values for Trigger.RespondMode.
const (
	RespondModeImmediately = "immediately"  // 202 at enqueue, fire-and-forget (default)
	RespondModeLastNode    = "last_node"    // block until workflow finishes, return last output
	RespondModeRespondNode = "respond_node" // block until webhook_respond node fires
)

const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusPaused  = "paused"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// RunState is the persisted execution snapshot.
type RunState struct {
	RunID      string                `json:"run_id"`
	WorkflowID string                `json:"workflow_id"`
	Version    int                   `json:"version"`
	Status     string                `json:"status"`
	Entry      string                `json:"entry"`
	Current    []string              `json:"current"`
	Completed  []string              `json:"completed"`
	Failed     []string              `json:"failed,omitempty"`
	Skipped    []string              `json:"skipped,omitempty"`
	Outputs    map[string]any        `json:"outputs"`
	Event      Event                 `json:"event"`
	Error      *NodeError            `json:"error,omitempty"`
	Sessions   map[string]SessionRec `json:"sessions,omitempty"`
	StartedAt  time.Time             `json:"started_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
	EndedAt    *time.Time            `json:"ended_at,omitempty"`
}

// NodeError captures a failed node's diagnostic.
type NodeError struct {
	Node    string `json:"node"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// SessionRec tracks a long-lived agent subprocess across nodes.
type SessionRec struct {
	PID            int       `json:"pid"`
	StartedAt      time.Time `json:"started_at"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	TranscriptPath string    `json:"transcript_path,omitempty"`
}

// RunEvent is one line in events.jsonl.
type RunEvent struct {
	TS    time.Time      `json:"ts"`
	Event string         `json:"event"`
	Node  string         `json:"node,omitempty"`
	Case  string         `json:"case,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// Event types emitted to events.jsonl.
const (
	EventNodeStarted       = "node_started"
	EventNodeCompleted     = "node_completed"
	EventNodeFailed        = "node_failed"
	EventNodeSkipped       = "node_skipped"
	EventEdgeTraversed     = "edge_traversed"
	EventWorkflowStarted   = "workflow_started"
	EventWorkflowCompleted = "workflow_completed"
	EventWorkflowFailed    = "workflow_failed"
)

// WorkflowState is the persisted approval/governance snapshot.
type WorkflowState struct {
	Approved        bool       `json:"approved"`
	ApprovedBy      string     `json:"approved_by,omitempty"`
	ApprovedAt      *time.Time `json:"approved_at,omitempty"`
	ApprovedVersion int        `json:"approved_version,omitempty"`
	ContentHash     string     `json:"content_hash,omitempty"`
	GovernanceMode  string     `json:"governance_mode,omitempty"`
	OverrideReason  string     `json:"override_reason,omitempty"`
}

// Override carries an optional human override when approving past a guard failure.
type Override struct {
	Reason string
	User   string
}
