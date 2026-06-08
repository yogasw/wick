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
	"encoding/json"
	"strings"
	"time"
)

// NodeType is the discriminator for the polymorphic Node body.
type NodeType string

const (
	NodeClassify      NodeType = "classify"
	NodeAgent         NodeType = "agent"
	NodeChannel       NodeType = "channel"
	NodeConnector     NodeType = "connector"
	NodeShell         NodeType = "shell"
	NodeSwitch        NodeType = "switch"
	NodeGoScript      NodeType = "go_script"
	NodePython        NodeType = "python"
	NodeHTTP          NodeType = "http"
	NodeDBQuery       NodeType = "db_query"
	NodeTransform     NodeType = "transform"
	NodeBranch        NodeType = "branch"
	NodeParallel      NodeType = "parallel"
	NodeMerge         NodeType = "merge"
	NodeEnd           NodeType = "end"
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
	ID             string             `yaml:"id"                       json:"id"`
	Version        int                `yaml:"version"                  json:"version"`
	Name           string             `yaml:"name"                     json:"name"`
	Description    string             `yaml:"description,omitempty"    json:"description,omitempty"`
	Enabled        bool               `yaml:"enabled"                  json:"enabled"`
	MaxDurationSec int                `yaml:"max_duration_sec,omitempty" json:"max_duration_sec,omitempty"`
	Triggers       []Trigger          `yaml:"triggers"                 json:"triggers"`
	Queue          QueuePolicy        `yaml:"queue,omitempty"          json:"queue,omitempty"`
	Env            []EnvField         `yaml:"env,omitempty"            json:"env,omitempty"`
	DataTables     []DataTableBinding `yaml:"data_tables,omitempty"    json:"data_tables,omitempty"`
	Graph          Graph              `yaml:"graph"                    json:"graph"`
	OnError        *OnErrorBinding    `yaml:"on_error,omitempty"       json:"on_error,omitempty"`
	CreatedBy      string             `yaml:"created_by,omitempty"     json:"created_by,omitempty"`
	CreatedAt      time.Time          `yaml:"created_at,omitempty"     json:"created_at,omitempty"`
	Canvas         map[string]any     `yaml:"_canvas,omitempty"        json:"_canvas,omitempty"`
}

// QueuePolicy controls per-workflow concurrency.
type QueuePolicy struct {
	MaxSize    int    `yaml:"max_size,omitempty"     json:"max_size,omitempty"`
	OnOverflow string `yaml:"on_overflow,omitempty"  json:"on_overflow,omitempty"`
}

// Overflow policy values.
const (
	OverflowDropOldest = "drop_oldest"
	OverflowDropNew    = "drop_new"
	OverflowReject     = "reject"
)

// Graph is the DAG body: flat node list + separate edge list.
type Graph struct {
	Entry string `yaml:"entry"  json:"entry"`
	Nodes []Node `yaml:"nodes"  json:"nodes"`
	Edges []Edge `yaml:"edges"  json:"edges"`
}

// Edge is a directed connection from one node to another. Case is
// only meaningful when From is a classify or branch node.
type Edge struct {
	From  string `yaml:"from"            json:"from"`
	To    string `yaml:"to"              json:"to"`
	Case  string `yaml:"case,omitempty"  json:"case,omitempty"`
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
}

// Node is a single step in the graph. Fields are a flat union — only
// the subset relevant to Type is read by the executor. Validator
// rejects nodes that set fields outside their type.
type Node struct {
	// Common
	ID           string         `yaml:"id"                       json:"id"`
	Type         NodeType       `yaml:"type"                     json:"type"`
	Label        string         `yaml:"label,omitempty"          json:"label,omitempty"`
	Description  string         `yaml:"description,omitempty"    json:"description,omitempty"`
	TimeoutSec   int            `yaml:"timeout_sec,omitempty"    json:"timeout_sec,omitempty"`
	Retry        *RetryPolicy   `yaml:"retry,omitempty"          json:"retry,omitempty"`
	OnFailure    string         `yaml:"on_failure,omitempty"     json:"on_failure,omitempty"`
	Fallback     string         `yaml:"fallback,omitempty"       json:"fallback,omitempty"`
	OutputSchema map[string]any `yaml:"output_schema,omitempty"  json:"output_schema,omitempty"`

	// parallel
	Branches []string `yaml:"branches,omitempty" json:"branches,omitempty"`

	// merge
	Inputs   []string `yaml:"inputs,omitempty"   json:"inputs,omitempty"`
	Strategy string   `yaml:"strategy,omitempty" json:"strategy,omitempty"`

	// classify + agent
	Provider string `yaml:"provider,omitempty"    json:"provider,omitempty"`
	Preset   string `yaml:"preset,omitempty"      json:"preset,omitempty"`
	Prompt   string `yaml:"prompt,omitempty"      json:"prompt,omitempty"`
	Session  string `yaml:"session,omitempty"     json:"session,omitempty"`

	// agent override — copy resolved sessionID from another node in
	// this run. Must reference an upstream agent or session_init node.
	SessionFrom string `yaml:"session_from,omitempty" json:"session_from,omitempty"`

	// session_init — preset shortcut OR rendered template id. Mutually
	// exclusive; SessionID wins when both set. `Preset` reuses the
	// classify/agent Preset field above for YAML brevity.
	SessionID string `yaml:"session_id,omitempty" json:"session_id,omitempty"`

	// classify
	OutputCases         []string          `yaml:"output_cases,omitempty"         json:"output_cases,omitempty"`
	StructuredOutput    *bool             `yaml:"structured_output,omitempty"    json:"structured_output,omitempty"`
	Normalize           *bool             `yaml:"normalize,omitempty"            json:"normalize,omitempty"`
	FuzzyMatch          bool              `yaml:"fuzzy_match,omitempty"          json:"fuzzy_match,omitempty"`
	RetryOnMismatch     int               `yaml:"retry_on_mismatch,omitempty"    json:"retry_on_mismatch,omitempty"`
	ConfidenceThreshold float64           `yaml:"confidence_threshold,omitempty" json:"confidence_threshold,omitempty"`
	Examples            []ClassifyExample `yaml:"examples,omitempty"             json:"examples,omitempty"`

	// agent
	Workspace     string   `yaml:"workspace,omitempty"      json:"workspace,omitempty"`
	Skills        []string `yaml:"skills,omitempty"         json:"skills,omitempty"`
	Tools         []string `yaml:"tools,omitempty"          json:"tools,omitempty"`
	MaxTurns      int      `yaml:"max_turns,omitempty"      json:"max_turns,omitempty"`
	RequireStatus bool     `yaml:"require_status,omitempty" json:"require_status,omitempty"`

	// channel (action) — Channel field name avoided clash with Event.Channel
	ChannelName string            `yaml:"channel,omitempty"   json:"channel,omitempty"`
	Op          string            `yaml:"op,omitempty"        json:"op,omitempty"`
	Args        map[string]any    `yaml:"args,omitempty"      json:"args,omitempty"`
	ArgModes    map[string]string `yaml:"arg_modes,omitempty" json:"arg_modes,omitempty"`

	// connector — uses row_id for instance (datatable_* nodes own `row:`)
	Module string `yaml:"module,omitempty" json:"module,omitempty"`
	Row    string `yaml:"row_id,omitempty" json:"row_id,omitempty"`

	// shell
	Command     []string          `yaml:"command,omitempty"      json:"command,omitempty"`
	ShellEnv    map[string]string `yaml:"env,omitempty"          json:"env,omitempty"`
	Cwd         string            `yaml:"cwd,omitempty"          json:"cwd,omitempty"`
	ParseOutput string            `yaml:"parse_output,omitempty" json:"parse_output,omitempty"`

	// http
	Method        string            `yaml:"method,omitempty"         json:"method,omitempty"`
	URL           string            `yaml:"url,omitempty"            json:"url,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"        json:"headers,omitempty"`
	Query         map[string]string `yaml:"query,omitempty"          json:"query,omitempty"`
	Body          string            `yaml:"body,omitempty"           json:"body,omitempty"`
	ParseResponse string            `yaml:"parse_response,omitempty" json:"parse_response,omitempty"`

	// db_query — uses `sql:` key (HTTP node already owns `query:` for query params)
	Database string   `yaml:"database,omitempty" json:"database,omitempty"`
	SQL      string   `yaml:"sql,omitempty"      json:"sql,omitempty"`
	SQLArgs  []string `yaml:"sql_args,omitempty" json:"sql_args,omitempty"`

	// transform
	Engine     string `yaml:"engine,omitempty"     json:"engine,omitempty"`
	Input      string `yaml:"input,omitempty"      json:"input,omitempty"`
	Expression string `yaml:"expression,omitempty" json:"expression,omitempty"`

	// go_script — full Go program. Engine pipes RenderCtx JSON to
	// stdin, parses stdout as JSON for the result.
	Code string `yaml:"code,omitempty" json:"code,omitempty"`

	// branch
	Expr string `yaml:"expr,omitempty" json:"expr,omitempty"`

	// switch — first-match-wins rule list. Each rule's `when` is a Go
	// template that renders to a bool (supports the same binary ops as
	// `branch`: ==, !=, <, <=, >, >=) or any non-empty string (truthy).
	// First rule whose `when` evaluates true wins; engine emits
	// Verdict=<rule.case> so the edge `case: <label>` filter routes
	// downstream. DefaultCase fires when no rule matches.
	Cases       []SwitchCase `yaml:"cases,omitempty"        json:"cases,omitempty"`
	DefaultCase string       `yaml:"default_case,omitempty" json:"default_case,omitempty"`

	// datatable_*
	Table          string              `yaml:"table,omitempty"           json:"table,omitempty"`
	Where          map[string]any      `yaml:"where,omitempty"           json:"where,omitempty"`
	Conditions     []DataTableCondYAML `yaml:"conditions,omitempty"      json:"conditions,omitempty"`
	ConditionModes map[string]string   `yaml:"condition_modes,omitempty" json:"condition_modes,omitempty"`
	RowModes       map[string]string   `yaml:"row_modes,omitempty"       json:"row_modes,omitempty"`
	Key            map[string]any      `yaml:"key,omitempty"             json:"key,omitempty"`
	RowValues      map[string]any      `yaml:"row,omitempty"             json:"row,omitempty"`
	OrderBy        []DataTableOrder    `yaml:"order_by,omitempty"        json:"order_by,omitempty"`
	Limit          int                 `yaml:"limit,omitempty"           json:"limit,omitempty"`
	Offset         int                 `yaml:"offset,omitempty"          json:"offset,omitempty"`

	// end
	Result string `yaml:"result,omitempty" json:"result,omitempty"`

	// webhook_respond — sends a custom HTTP response back to the webhook caller.
	// Requires the trigger's respond_mode = "respond_node".
	RespondStatus  int               `yaml:"respond_status,omitempty"  json:"respond_status,omitempty"`
	RespondBody    string            `yaml:"respond_body,omitempty"    json:"respond_body,omitempty"`
	RespondHeaders map[string]string `yaml:"respond_headers,omitempty" json:"respond_headers,omitempty"`
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
	From string `yaml:"from,omitempty" json:"from,omitempty"`
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// RetryPolicy on a node.
type RetryPolicy struct {
	Max        int `yaml:"max"                   json:"max"`
	BackoffSec int `yaml:"backoff_sec,omitempty" json:"backoff_sec,omitempty"`
}

// ClassifyExample is a few-shot prompt example.
type ClassifyExample struct {
	Input  string `yaml:"input"  json:"input"`
	Output string `yaml:"output" json:"output"`
}

// SwitchCase is one rule row for `switch` nodes.
type SwitchCase struct {
	When string `yaml:"when" json:"when"`
	Case string `yaml:"case" json:"case"`
}

// DataTableOrder is one order-by clause.
type DataTableOrder struct {
	Column    string `yaml:"column"              json:"column"`
	Direction string `yaml:"direction,omitempty" json:"direction,omitempty"`
}

// DataTableCondYAML is one condition row declared in a workflow.
// Name kept for backward compatibility; storage is JSON.
type DataTableCondYAML struct {
	Column string `yaml:"column"          json:"column"`
	Op     string `yaml:"op"              json:"op"`
	Value  any    `yaml:"value,omitempty" json:"value,omitempty"`
}

// EnvField is one entry of the workflow's env schema.
type EnvField struct {
	Name        string            `yaml:"name"                  json:"name"`
	Widget      string            `yaml:"widget,omitempty"      json:"widget,omitempty"`
	Desc        string            `yaml:"desc,omitempty"        json:"desc,omitempty"`
	Default     string            `yaml:"default,omitempty"     json:"default,omitempty"`
	Required    bool              `yaml:"required,omitempty"    json:"required,omitempty"`
	Locked      bool              `yaml:"locked,omitempty"      json:"locked,omitempty"`
	Hidden      bool              `yaml:"hidden,omitempty"      json:"hidden,omitempty"`
	Options     []EnvOption       `yaml:"options,omitempty"     json:"options,omitempty"`
	VisibleWhen map[string]string `yaml:"visible_when,omitempty" json:"visible_when,omitempty"`
}

// IsSecret reports whether this field is the encrypted variant.
func (f EnvField) IsSecret() bool { return f.Widget == "secret" }

// EnvOption is one choice for dropdown/picker widgets.
type EnvOption struct {
	ID   string `yaml:"id"   json:"id"`
	Name string `yaml:"name" json:"name"`
}

// DataTableBinding wires a workflow-local alias to a data table slug.
type DataTableBinding struct {
	Name string `yaml:"name"           json:"name"`
	Ref  string `yaml:"ref"            json:"ref"`
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// Trigger is one polymorphic trigger entry. Fields are a flat union
// like Node — the validator gates each field to its Type.
type Trigger struct {
	ID        string      `yaml:"id,omitempty"         json:"id,omitempty"`
	Type      TriggerType `yaml:"type"                 json:"type"`
	EntryNode string      `yaml:"entry_node,omitempty" json:"entry_node,omitempty"`

	// cron
	Schedule string `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`

	// channel
	ChannelName  string            `yaml:"channel,omitempty"        json:"channel,omitempty"`
	Event        string            `yaml:"event,omitempty"          json:"event,omitempty"`
	Target       string            `yaml:"target,omitempty"         json:"target,omitempty"`
	Match        map[string]any    `yaml:"match,omitempty"          json:"match,omitempty"`
	MatchEnabled bool              `yaml:"match_enabled,omitempty"  json:"match_enabled,omitempty"`
	MatchModes   map[string]string `yaml:"match_modes,omitempty"    json:"match_modes,omitempty"`
	Whitelist    *Whitelist        `yaml:"whitelist,omitempty"      json:"whitelist,omitempty"`
	DedupTTLSec  int               `yaml:"dedup_ttl_sec,omitempty"  json:"dedup_ttl_sec,omitempty"`
	ReplySource  *bool             `yaml:"reply_source,omitempty"   json:"reply_source,omitempty"`

	// webhook
	Path        string `yaml:"path,omitempty"         json:"path,omitempty"`
	Method      string `yaml:"method,omitempty"       json:"method,omitempty"`
	SecretRef   string `yaml:"secret_ref,omitempty"   json:"secret_ref,omitempty"`
	ParseBody   string `yaml:"parse_body,omitempty"   json:"parse_body,omitempty"`
	BodyToVar   string `yaml:"body_to_var,omitempty"  json:"body_to_var,omitempty"`
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
	RespondMode string `yaml:"respond_mode,omitempty" json:"respond_mode,omitempty"`

	// manual
	Label       string `yaml:"label,omitempty"        json:"label,omitempty"`
	ButtonLabel string `yaml:"button_label,omitempty" json:"button_label,omitempty"`
	RequireRole string `yaml:"require_role,omitempty" json:"require_role,omitempty"`

	// schedule_at
	At          time.Time `yaml:"at,omitempty"           json:"at,omitempty"`
	DeleteAfter bool      `yaml:"delete_after,omitempty" json:"delete_after,omitempty"`

	// error
	SourceWorkflow string   `yaml:"source_workflow,omitempty" json:"source_workflow,omitempty"`
	Severity       []string `yaml:"severity,omitempty"        json:"severity,omitempty"`
	NodeTypes      []string `yaml:"node_types,omitempty"      json:"node_types,omitempty"`
}

// MarshalYAML normalizes Match before serialization — picker values stored as
// JSON strings (`[{"id":"C1","name":"#ch"}]`) are expanded to native slices
// so the output is AI-writable without JSON escaping.
func (tr Trigger) MarshalYAML() (any, error) {
	type plain Trigger
	p := plain(tr)
	if len(p.Match) > 0 {
		norm := make(map[string]any, len(p.Match))
		for k, v := range p.Match {
			s, ok := v.(string)
			if !ok {
				norm[k] = v
				continue
			}
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, "[") {
				var arr []map[string]any
				if err := json.Unmarshal([]byte(s), &arr); err == nil {
					norm[k] = arr
					continue
				}
			}
			norm[k] = v
		}
		p.Match = norm
	}
	return p, nil
}

// Whitelist filters who can fire a trigger.
type Whitelist struct {
	Users  []string `yaml:"users,omitempty"  json:"users,omitempty"`
	Groups []string `yaml:"groups,omitempty" json:"groups,omitempty"`
	IPs    []string `yaml:"ips,omitempty"    json:"ips,omitempty"`
}

// OnErrorBinding declares which error-handler workflow to fire on failure.
type OnErrorBinding struct {
	TriggerWorkflow   string `yaml:"trigger_workflow"               json:"trigger_workflow"`
	Severity          string `yaml:"severity,omitempty"             json:"severity,omitempty"`
	IncludeState      bool   `yaml:"include_state,omitempty"        json:"include_state,omitempty"`
	IncludeNodeOutput bool   `yaml:"include_node_output,omitempty"  json:"include_node_output,omitempty"`
}

// Event is the trigger payload passed to a run.
type Event struct {
	Type    string         `json:"type"`
	Subtype string         `json:"subtype,omitempty"`
	Channel string         `json:"channel,omitempty"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
	TriggerID string `json:"trigger_id,omitempty"`
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
