package entity

import "time"

// Workflow is the DB representation of a workflow document. Body
// columns hold the canonical JSON; metadata columns (Name, Enabled)
// duplicate body fields so the list page can render without parsing
// every row.
type Workflow struct {
	// ID is the stable UUID minted by the canvas / MCP create flow.
	// Matches Workflow.ID on the in-memory side.
	ID      string `gorm:"primaryKey;type:varchar(64)"`
	Name    string `gorm:"type:varchar(256);not null;default:''"`
	Enabled bool   `gorm:"not null;default:false"`
	Version int    `gorm:"not null;default:0"`
	// BodyPublished is the last-published JSON body. Mutated only by
	// the publish path. Empty until first publish.
	BodyPublished string `gorm:"type:text;not null;default:''"`
	// BodyDraft is the in-progress edit. Mutated on every save. Empty
	// when there is no draft (cleared on publish + discard).
	BodyDraft string `gorm:"type:text;not null;default:''"`
	HasDraft  bool   `gorm:"not null;default:false"`
	CreatedBy string `gorm:"type:varchar(128);default:''"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Workflow) TableName() string { return "workflows" }

// WorkflowVersion captures one immutable snapshot of a workflow at a
// point in time. Two flavours:
//   - Kind = "draft"     — written on every save while the user is
//                          editing. Retention policy: keep the last N
//                          per workflow (default 50, configurable).
//   - Kind = "published" — written on Publish. Retained forever; this
//                          is the audit trail the History UI surfaces
//                          as restorable revisions.
//
// `Message` is an optional human-readable label users attach when they
// publish ("fix slack template", "add retry"). `CreatedBy` is the
// authenticated user id captured by the handler.
type WorkflowVersion struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	WorkflowID string `gorm:"type:varchar(64);not null;index"`
	Kind       string `gorm:"type:varchar(16);not null;index"` // "draft" | "published"
	Body       string `gorm:"type:text;not null"`
	Message    string `gorm:"type:varchar(512);default:''"`
	CreatedBy  string `gorm:"type:varchar(128);default:''"`
	CreatedAt  time.Time
}

func (WorkflowVersion) TableName() string { return "workflow_versions" }

// WorkflowTestCase mirrors the file-based `__tests__/<name>.json`
// fixtures. Migrated alongside the body so workflow tests survive the
// move and stay editable through the SPA.
type WorkflowTestCase struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	WorkflowID string `gorm:"type:varchar(64);not null;index:idx_wtc_workflow_name,unique,priority:1"`
	Name       string `gorm:"type:varchar(256);not null;index:idx_wtc_workflow_name,unique,priority:2"`
	Body       string `gorm:"type:text;not null"`
	UpdatedAt  time.Time
}

func (WorkflowTestCase) TableName() string { return "workflow_test_cases" }

