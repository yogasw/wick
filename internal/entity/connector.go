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
// (Loki Prod, Loki Staging, Loki Dev). Uniqueness lives on ID.
//
// Two distinct lineage fields:
//
//   - Key points to the code definition this row was instantiated from.
//     Set at creation, immutable. Same role as ToolPath in tool-side
//     entities, just keyed by the code slug instead of a URL path.
//   - ParentID points to the source Connector.ID this row was
//     duplicated from. Nil for root rows created from scratch
//     (whether via UI "+ New" or via a code-side registration). Set
//     when an admin clicks Duplicate in the UI. Informational only —
//     no behavior is tied to the parent link, and there is no FK
//     constraint, so the parent may be deleted independently (the
//     child becomes a dangling reference, which the UI tolerates).
//
// Configs holds the credential / endpoint values as a JSON-encoded
// map[string]string keyed by the field names declared on the
// connector's Creds struct. Secret-marked fields are encrypted at rest
// before being written to this column. The jsonb shape is preferred to
// a row-per-field table because cred sets are tiny, always read together,
// and never need cross-instance queries.
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
	ID        string  `gorm:"type:varchar(36);primaryKey"`
	Key       string  `gorm:"type:varchar(100);index;not null"`
	ParentID  *string `gorm:"type:varchar(36);index"`
	Label     string  `gorm:"type:varchar(255);not null"`
	Configs   string  `gorm:"type:text"`
	Disabled  bool    `gorm:"default:false"`
	CreatedBy string  `gorm:"type:varchar(36)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (c *Connector) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
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
