package entity

import (
	"time"
)

// DataTable is the schema metadata row, one per logical data table.
// SchemaJSON carries the full Schema body (columns with stable ids,
// next_col_id counter, access, retention) — see
// internal/agents/workflow/datatable for the typed shape.
//
// All data rows live in DataTableRow keyed by Slug. There are no
// per-table physical tables — wick stays at two tables total
// (wick_data_tables + wick_data_table_rows) regardless of how many
// logical tables a user creates.
type DataTable struct {
	Slug        string `gorm:"type:varchar(80);primaryKey"`
	Name        string `gorm:"type:varchar(255);not null"`
	Description string `gorm:"type:text"`
	Mode        string `gorm:"type:varchar(20);not null;default:'strict'"` // strict | lax
	SchemaJSON  string `gorm:"type:jsonb;not null;default:'{}'"`
	AccessJSON  string `gorm:"type:jsonb;default:'{}'"`
	NextRowID   int64  `gorm:"not null;default:0"` // per-slug monotonic row id allocator
	CreatedBy   string `gorm:"type:varchar(36)"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (DataTable) TableName() string { return "wick_data_tables" }

// DataTableRow is one row of one logical data table. Data is JSONB
// keyed by column id (the stable "cN" identifier from the schema),
// so renaming a column updates only the schema row, never the data.
//
// Primary key is composite (TableSlug, ID). ID is allocated by the
// app from DataTable.NextRowID under SELECT FOR UPDATE so concurrent
// inserts never collide.
type DataTableRow struct {
	TableSlug string    `gorm:"type:varchar(80);primaryKey;not null;index:idx_dtr_slug_created,priority:1"`
	ID        int64     `gorm:"primaryKey;not null;autoIncrement:false"`
	Data      string    `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt time.Time `gorm:"not null;index:idx_dtr_slug_created,priority:2,sort:desc"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (DataTableRow) TableName() string { return "wick_data_table_rows" }
