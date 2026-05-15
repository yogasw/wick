// Package workflow is the domain for AI-orchestrated multi-step
// automations stored at `<BaseDir>/workflows/<slug>/`. A workflow is a
// directed acyclic graph of typed nodes (classify/agent/connector/
// shell/http/branch/parallel/merge/dataset_*/transform/end) with one
// or more triggers (cron, channel, webhook, manual, schedule_at,
// error). The engine walks the graph node-by-node, persists state per
// run, and reuses existing wick infra (channels, connectors, providers,
// pool).
//
// See `internal/docs/workflow-design.md` for the full contract.
package workflow

import (
	"time"
)

// NodeType is the discriminator for the polymorphic Node body.
type NodeType string

const (
	NodeClassify       NodeType = "classify"
	NodeAgent          NodeType = "agent"
	NodeChannel        NodeType = "channel"
	NodeConnector      NodeType = "connector"
	NodeShell          NodeType = "shell"
	NodePython         NodeType = "python"
	NodeHTTP           NodeType = "http"
	NodeDBQuery        NodeType = "db_query"
	NodeTransform      NodeType = "transform"
	NodeBranch         NodeType = "branch"
	NodeParallel       NodeType = "parallel"
	NodeMerge          NodeType = "merge"
	NodeEnd            NodeType = "end"
	NodeDatasetGet     NodeType = "dataset_get"
	NodeDatasetExists  NodeType = "dataset_exists"
	NodeDatasetQuery   NodeType = "dataset_query"
	NodeDatasetInsert  NodeType = "dataset_insert"
	NodeDatasetUpsert  NodeType = "dataset_upsert"
	NodeDatasetDelete  NodeType = "dataset_delete"
	NodeDatasetCount   NodeType = "dataset_count"
)

// IsDatasetNode reports whether t is one of the dataset_* variants.
func (t NodeType) IsDatasetNode() bool {
	switch t {
	case NodeDatasetGet, NodeDatasetExists, NodeDatasetQuery,
		NodeDatasetInsert, NodeDatasetUpsert, NodeDatasetDelete, NodeDatasetCount:
		return true
	}
	return false
}

// IsBranchSource reports whether nodes of this type produce a verdict
// that filters outgoing edges by `case:`.
func (t NodeType) IsBranchSource() bool {
	return t == NodeClassify || t == NodeBranch
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

// Workflow is the root document parsed from `workflow.yaml`.
type Workflow struct {
	ID             string           `yaml:"id"`
	Slug           string           `yaml:"-"` // derived from folder name
	Version        int              `yaml:"version"`
	Name           string           `yaml:"name"`
	Description    string           `yaml:"description,omitempty"`
	Enabled        bool             `yaml:"enabled"`
	MaxDurationSec int              `yaml:"max_duration_sec,omitempty"`
	Triggers       []Trigger        `yaml:"triggers"`
	Queue          QueuePolicy      `yaml:"queue,omitempty"`
	Env            []EnvField       `yaml:"env,omitempty"`
	Datasets       []DatasetBinding `yaml:"datasets,omitempty"`
	Graph          Graph            `yaml:"graph"`
	OnError        *OnErrorBinding  `yaml:"on_error,omitempty"`
	CreatedBy      string           `yaml:"created_by,omitempty"`
	CreatedAt      time.Time        `yaml:"created_at,omitempty"`
	Canvas         map[string]any   `yaml:"_canvas,omitempty"`
}

// QueuePolicy controls per-workflow concurrency.
type QueuePolicy struct {
	MaxSize    int    `yaml:"max_size,omitempty"`
	OnOverflow string `yaml:"on_overflow,omitempty"` // drop_oldest | drop_new | reject
}

// Overflow policy values.
const (
	OverflowDropOldest = "drop_oldest"
	OverflowDropNew    = "drop_new"
	OverflowReject     = "reject"
)

// Graph is the DAG body: flat node list + separate edge list.
type Graph struct {
	Entry string `yaml:"entry"`
	Nodes []Node `yaml:"nodes"`
	Edges []Edge `yaml:"edges"`
}

// Edge is a directed connection from one node to another. Case is
// only meaningful when From is a classify or branch node.
type Edge struct {
	From  string `yaml:"from"`
	To    string `yaml:"to"`
	Case  string `yaml:"case,omitempty"`
	Label string `yaml:"label,omitempty"`
}

// Node is a single step in the graph. Fields are a flat union — only
// the subset relevant to Type is read by the executor. Validator
// rejects nodes that set fields outside their type.
type Node struct {
	// Common
	ID           string         `yaml:"id"`
	Type         NodeType       `yaml:"type"`
	Label        string         `yaml:"label,omitempty"`
	Description  string         `yaml:"description,omitempty"`
	TimeoutSec   int            `yaml:"timeout_sec,omitempty"`
	Retry        *RetryPolicy   `yaml:"retry,omitempty"`
	OnFailure    string         `yaml:"on_failure,omitempty"`
	Fallback     string         `yaml:"fallback,omitempty"`
	OutputSchema map[string]any `yaml:"output_schema,omitempty"`

	// parallel
	Branches []string `yaml:"branches,omitempty"`

	// merge
	Inputs   []string `yaml:"inputs,omitempty"`
	Strategy string   `yaml:"strategy,omitempty"`

	// classify + agent
	Provider   string `yaml:"provider,omitempty"`
	Preset     string `yaml:"preset,omitempty"`
	Prompt     string `yaml:"prompt,omitempty"`
	PromptFile string `yaml:"prompt_file,omitempty"`
	Session    string `yaml:"session,omitempty"`

	// classify
	OutputCases         []string          `yaml:"output_cases,omitempty"`
	StructuredOutput    *bool             `yaml:"structured_output,omitempty"`
	Normalize           *bool             `yaml:"normalize,omitempty"`
	FuzzyMatch          bool              `yaml:"fuzzy_match,omitempty"`
	RetryOnMismatch     int               `yaml:"retry_on_mismatch,omitempty"`
	ConfidenceThreshold float64           `yaml:"confidence_threshold,omitempty"`
	Examples            []ClassifyExample `yaml:"examples,omitempty"`

	// agent
	Workspace string   `yaml:"workspace,omitempty"`
	Skills    []string `yaml:"skills,omitempty"`
	Tools     []string `yaml:"tools,omitempty"`
	MaxTurns  int      `yaml:"max_turns,omitempty"`

	// channel (action) — Channel field name avoided clash with Event.Channel
	ChannelName string         `yaml:"channel,omitempty"`
	Op          string         `yaml:"op,omitempty"`
	Args        map[string]any `yaml:"args,omitempty"`
	// ArgModes records each arg's editor mode: "fixed" = literal value
	// (executor skips template render), "expression" = Go template (the
	// default behaviour kept for backward compat when ArgModes has no
	// entry for a key). Persisted so the inspector restores the toggle
	// state and so safer-by-default semantics survive a publish round
	// trip. Defaults to template render when an arg key is missing
	// here, matching pre-ArgModes workflows.
	ArgModes map[string]string `yaml:"arg_modes,omitempty"`

	// connector — uses row_id for instance (dataset_* nodes own `row:`)
	Module string `yaml:"module,omitempty"`
	Row    string `yaml:"row_id,omitempty"`

	// shell
	Command     []string          `yaml:"command,omitempty"`
	ShellEnv    map[string]string `yaml:"env,omitempty"`
	Cwd         string            `yaml:"cwd,omitempty"`
	ParseOutput string            `yaml:"parse_output,omitempty"`

	// http
	Method        string            `yaml:"method,omitempty"`
	URL           string            `yaml:"url,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty"`
	Query         map[string]string `yaml:"query,omitempty"`
	Body          string            `yaml:"body,omitempty"`
	ParseResponse string            `yaml:"parse_response,omitempty"`

	// db_query — uses `sql:` key (HTTP node already owns `query:` for query params)
	Database string   `yaml:"database,omitempty"`
	SQL      string   `yaml:"sql,omitempty"`
	SQLArgs  []string `yaml:"sql_args,omitempty"`

	// transform
	Engine     string `yaml:"engine,omitempty"`
	Input      string `yaml:"input,omitempty"`
	Expression string `yaml:"expression,omitempty"`

	// branch
	Expr string `yaml:"expr,omitempty"`

	// dataset_*
	Dataset    string         `yaml:"dataset,omitempty"`
	Where      map[string]any `yaml:"where,omitempty"`
	Key        map[string]any `yaml:"key,omitempty"`
	RowValues  map[string]any `yaml:"row,omitempty"`
	OrderBy    []DatasetOrder `yaml:"order_by,omitempty"`
	Limit      int            `yaml:"limit,omitempty"`
	Offset     int            `yaml:"offset,omitempty"`

	// end
	Result string `yaml:"result,omitempty"`
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
const (
	SessionNew        = "new"
	SessionRoot       = "root"
	SessionPersistent = "persistent"
)

// RetryPolicy on a node.
type RetryPolicy struct {
	Max        int `yaml:"max"`
	BackoffSec int `yaml:"backoff_sec,omitempty"`
}

// ClassifyExample is a few-shot prompt example.
type ClassifyExample struct {
	Input  string `yaml:"input"`
	Output string `yaml:"output"`
}

// DatasetOrder is one order-by clause.
type DatasetOrder struct {
	Column    string `yaml:"column"`
	Direction string `yaml:"direction,omitempty"`
}

// EnvField is one entry of the workflow's env schema.
type EnvField struct {
	Name        string            `yaml:"name"`
	Widget      string            `yaml:"widget,omitempty"`
	Desc        string            `yaml:"desc,omitempty"`
	Default     string            `yaml:"default,omitempty"`
	Required    bool              `yaml:"required,omitempty"`
	Locked      bool              `yaml:"locked,omitempty"`
	Hidden      bool              `yaml:"hidden,omitempty"`
	Options     []EnvOption       `yaml:"options,omitempty"`
	VisibleWhen map[string]string `yaml:"visible_when,omitempty"`
}

// IsSecret reports whether this field is the encrypted variant.
func (f EnvField) IsSecret() bool { return f.Widget == "secret" }

// EnvOption is one choice for dropdown/picker widgets.
type EnvOption struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// DatasetBinding wires a dataset alias to a dataset slug.
type DatasetBinding struct {
	Name            string `yaml:"name"`
	Ref             string `yaml:"ref"`
	Mode            string `yaml:"mode,omitempty"`
	ExpectedVersion int    `yaml:"expected_version,omitempty"`
}

// Trigger is one polymorphic trigger entry. Fields are a flat union
// like Node — the validator gates each field to its Type.
//
// ID is the stable canvas identifier (e.g. "trigger-manual",
// "trigger-cron-2"). The codec uses it to merge per-trigger
// metadata (channel name, schedule, …) across save cycles so the
// canvas can re-wire EntryNode without losing the config the user
// typed in the inspector. Optional in YAML — workflows hand-edited
// without canvas can omit it.
type Trigger struct {
	ID        string      `yaml:"id,omitempty"`
	Type      TriggerType `yaml:"type"`
	EntryNode string      `yaml:"entry_node,omitempty"`

	// cron
	Schedule string `yaml:"schedule,omitempty"`
	Timezone string `yaml:"timezone,omitempty"`

	// channel
	ChannelName string         `yaml:"channel,omitempty"`
	Event       string         `yaml:"event,omitempty"`
	Target      string         `yaml:"target,omitempty"`
	Match       map[string]any `yaml:"match,omitempty"`
	Whitelist   *Whitelist     `yaml:"whitelist,omitempty"`
	DedupTTLSec int            `yaml:"dedup_ttl_sec,omitempty"`
	ReplySource *bool          `yaml:"reply_source,omitempty"`

	// webhook
	Path      string `yaml:"path,omitempty"`
	Method    string `yaml:"method,omitempty"`
	SecretRef string `yaml:"secret_ref,omitempty"`
	ParseBody string `yaml:"parse_body,omitempty"`
	BodyToVar string `yaml:"body_to_var,omitempty"`

	// manual
	Label       string `yaml:"label,omitempty"`
	RequireRole string `yaml:"require_role,omitempty"`

	// schedule_at
	At          time.Time `yaml:"at,omitempty"`
	DeleteAfter bool      `yaml:"delete_after,omitempty"`

	// error
	SourceWorkflow string   `yaml:"source_workflow,omitempty"`
	Severity       []string `yaml:"severity,omitempty"`
	NodeTypes      []string `yaml:"node_types,omitempty"`
}

// Whitelist filters who can fire a trigger.
type Whitelist struct {
	Users  []string `yaml:"users,omitempty"`
	Groups []string `yaml:"groups,omitempty"`
	IPs    []string `yaml:"ips,omitempty"`
}

// OnErrorBinding declares which error-handler workflow to fire on failure.
type OnErrorBinding struct {
	TriggerWorkflow   string `yaml:"trigger_workflow"`
	Severity          string `yaml:"severity,omitempty"`
	IncludeState      bool   `yaml:"include_state,omitempty"`
	IncludeNodeOutput bool   `yaml:"include_node_output,omitempty"`
}

// Event is the trigger payload passed to a run. Minimal envelope —
// channel/transport-specific keys (user, text, thread, chat_id,
// callback_id, path, …) live in Payload so each integration owns its
// own shape. Workflow templates reference them as
// `{{.Event.Payload.<key>}}`.
//
// Type identifies the trigger family ("channel" | "webhook" | "cron" |
// "manual" | "error" | "schedule_at"). Subtype is the within-family
// discriminator: for channel events it's the event name
// ("message", "block_action", …); for webhooks empty; for cron the
// schedule expression label; for error the source workflow.
//
// Channel is the module name when Type=="channel" ("slack",
// "telegram", …). Empty for non-channel triggers.
type Event struct {
	Type    string         `json:"type"`
	Subtype string         `json:"subtype,omitempty"`
	Channel string         `json:"channel,omitempty"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// RunStatus values.
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
	Slug       string                `json:"slug"`
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
	EventNodeStarted        = "node_started"
	EventNodeCompleted      = "node_completed"
	EventNodeFailed         = "node_failed"
	EventNodeSkipped        = "node_skipped"
	EventEdgeTraversed      = "edge_traversed"
	EventWorkflowStarted    = "workflow_started"
	EventWorkflowCompleted  = "workflow_completed"
	EventWorkflowFailed     = "workflow_failed"
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
