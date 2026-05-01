package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Connector is one row per running connector — the runtime pairing of
// a code-registered connector definition (identified by Key) with a
// credential set, label, and creator.
//
// Connector definitions live in code (see pkg/connector and the
// internal/connectors registry); this entity is what the admin UI
// reads, writes, and duplicates. MCP exposes one tool per Connector
// row per enabled operation.
//
// Key references the code definition's slug (e.g. "loki", "github") —
// it is NOT unique on this table. Multiple Connector rows share the
// same Key when admins duplicate a definition into multiple instances
// (Loki Prod, Loki Staging, Loki Dev). Uniqueness lives on ID; the
// admin UI distinguishes siblings by Label.
//
// Code-registered Modules survive deletion: when bootstrap runs and
// finds zero rows for a registered Key, it auto-creates a fresh row
// (empty Configs, Label = Meta.Name). Admins who delete every row for
// a connector therefore get an empty-but-working row back on restart;
// duplicates and edits to existing rows are untouched.
//
// Configs holds the credential / endpoint values as a JSON-encoded
// map[string]string keyed by the field names declared on the
// connector's Creds struct. Secret-marked fields are stored plaintext
// (matching the wick `configs` table convention) and masked in the
// UI render layer; if at-rest encryption becomes a requirement it
// applies to both this column and the legacy configs table together.
// The jsonb shape is preferred to a row-per-field table because cred
// sets are tiny, always read together, and never need cross-instance
// queries.
//
// Disabled hides the row from MCP tools/list and the admin UI list view
// (admins can re-enable from the manager). The tag-filter system (the
// existing ToolTag table, addressed by path "/connectors/{id}", joined
// against UserTag) gates which authenticated users see this row at all
// — Disabled is the orthogonal "off switch" for the whole row.
//
// Tag association reuses ToolTag (with ToolPath = "/connectors/{id}")
// rather than introducing a connector-specific link table; jobs do the
// same with "/jobs/{path}". A future rename of ToolTag/SetToolTags into
// a generic entity-tag API is tracked separately.
type Connector struct {
	ID        string `gorm:"type:varchar(36);primaryKey"`
	Key       string `gorm:"type:varchar(100);index;not null"`
	Label     string `gorm:"type:varchar(255);not null"`
	Configs   string `gorm:"type:text"`
	Disabled  bool   `gorm:"default:false"`
	CreatedBy string `gorm:"type:varchar(36)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (c *Connector) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

// ConnectorRunSource describes how a ConnectorRun was triggered.
type ConnectorRunSource string

const (
	// ConnectorRunSourceMCP marks runs initiated by an LLM client through
	// the /mcp endpoint.
	ConnectorRunSourceMCP ConnectorRunSource = "mcp"
	// ConnectorRunSourceTest marks runs initiated from the panel-test
	// view in the admin UI (Postman-style manual exec).
	ConnectorRunSourceTest ConnectorRunSource = "test"
	// ConnectorRunSourceRetry marks runs that replay the request payload
	// of a previous run, identified by ConnectorRun.ParentRunID.
	ConnectorRunSourceRetry ConnectorRunSource = "retry"
)

// ConnectorRunStatus describes the outcome of a ConnectorRun.
type ConnectorRunStatus string

const (
	ConnectorRunStatusRunning ConnectorRunStatus = "running"
	ConnectorRunStatusSuccess ConnectorRunStatus = "success"
	ConnectorRunStatusError   ConnectorRunStatus = "error"
)

// ConnectorRun records one execution of one operation on one connector
// row. Written once per MCP tools/call, panel-test click, or retry, so
// admins can audit traffic, debug failures, and replay buggy calls.
//
// RequestJSON stores the input arguments the caller (LLM or admin)
// passed in. Credentials are NOT in this column — they live on the
// Connector row itself, joined at exec time. Replaying a run rebuilds
// the call from RequestJSON + the current Connector.Configs, so a
// retry honors any cred edits the admin has made since the original.
//
// ResponseJSON is the JSON-marshaled return value of ExecuteFunc.
// Large responses may be truncated by the writer (defense in depth);
// readers should treat the value as opaque.
//
// IPAddress and UserAgent capture the calling client's network
// identity at the time of the run. These are recorded for security
// observability — feeding a future allowlist/blocklist surface — and
// for incident triage. They are best-effort: behind a proxy the IP
// is whatever X-Forwarded-For policy the deploy resolves to, and a
// PAT-using script may not send a recognizable UA at all.
//
// ParentRunID is non-nil only when Source == ConnectorRunSourceRetry,
// pointing to the run this one re-played. There is no FK constraint —
// the parent may be deleted by retention, leaving the lineage dangling
// (the UI tolerates the gap).
//
// Retention: rows older than the configured retention window are
// purged by a scheduled cleanup job (default 7 days). The single-column
// index on StartedAt keeps the purge query cheap.
//
// Index strategy (composite, listed by query they serve):
//
//   - (connector_id, started_at DESC)  → "recent runs for this connector"
//   - (user_id, started_at DESC)       → "user activity timeline"
//   - (status, started_at DESC)        → "recent errors" filter
//   - (ip_address, started_at DESC)    → "activity from this IP" (future allow/block UX)
//   - started_at                       → retention purge
//   - parent_run_id                    → retry lineage trace
type ConnectorRun struct {
	ID           string             `gorm:"type:varchar(36);primaryKey"`
	ConnectorID  string             `gorm:"type:varchar(36);not null;index:idx_run_connector_started,priority:1"`
	OperationKey string             `gorm:"type:varchar(100);not null"`
	UserID       string             `gorm:"type:varchar(36);index:idx_run_user_started,priority:1"`
	Source       ConnectorRunSource `gorm:"type:varchar(20);not null"`
	RequestJSON  string             `gorm:"type:text"`
	ResponseJSON string             `gorm:"type:text"`
	Status       ConnectorRunStatus `gorm:"type:varchar(20);not null;index:idx_run_status_started,priority:1"`
	ErrorMsg     string             `gorm:"type:text"`
	LatencyMs    int
	HTTPStatus   int
	IPAddress    string    `gorm:"type:varchar(45);index:idx_run_ip_started,priority:1"`
	UserAgent    string    `gorm:"type:varchar(512)"`
	ParentRunID  *string   `gorm:"type:varchar(36);index"`
	StartedAt    time.Time `gorm:"not null;index;index:idx_run_connector_started,priority:2,sort:desc;index:idx_run_user_started,priority:2,sort:desc;index:idx_run_status_started,priority:2,sort:desc;index:idx_run_ip_started,priority:2,sort:desc"`
	EndedAt      *time.Time
	CreatedAt    time.Time
}

func (r *ConnectorRun) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

// ConnectorOperation stores the enable state of one operation on one
// connector row. Operations are declared in code (Module.Operations);
// this table records whether the admin opted them in (or out) for a
// specific connector row.
//
// Default rule applied when a connector row is created:
//   - Operation.Destructive == false → Enabled = true
//   - Operation.Destructive == true  → Enabled = false (admin opt-in)
//
// Rows for ops the admin has not touched yet may be missing; readers
// fall back to the default rule above when no row exists. Toggling in
// the UI inserts or updates a row.
type ConnectorOperation struct {
	ConnectorID  string `gorm:"primaryKey;type:varchar(36)"`
	OperationKey string `gorm:"primaryKey;type:varchar(100)"`
	Enabled      bool   `gorm:"default:true"`
	UpdatedAt    time.Time
}
