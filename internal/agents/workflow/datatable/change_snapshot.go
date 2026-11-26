package datatable

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

const MaxSnapshotRows = 10000
const MaxSnapshotBytes = 16 << 20

var ErrChangeSnapshotTooLarge = errors.New("change feed: snapshot exceeds bounded export limit")

// TableSnapshot is a trusted local export. IDs and ACLs are source-local,
// not portable authority. The consumer must authorize the complete table,
// map IDs and permissions, and atomically apply/verify receipt before ACK.
// Raw schema/data JSON preserves fields newer than this process knows.
type TableSnapshot struct {
	Version int `json:"version"`
	Slug string `json:"slug"`
	Watermark int64 `json:"watermark"`
	Table *entity.DataTable `json:"table,omitempty"`
	Rows []entity.DataTableRow `json:"rows"`
}

// Snapshot reads the scope watermark, schema and rows in the SAME database
// snapshot. SQLite's read transaction and PostgreSQL REPEATABLE READ avoid
// mixing a watermark with newer data. No raw database copy or remote API.
// A deleted table is represented by nil Table and empty Rows; an unselected
// scope is never exported. Large scopes need a future paginated protocol,
// not a silently truncated snapshot.
func (f *ChangeFeed) Snapshot(ctx context.Context, slug string, maxRows, maxBytes int) (TableSnapshot, error) {
	if err := parse.ValidateID(slug); err != nil { return TableSnapshot{},err }
	if maxRows < 1 || maxRows > MaxSnapshotRows || maxBytes < 1 || maxBytes > MaxSnapshotBytes {
		return TableSnapshot{},fmt.Errorf("change feed: invalid snapshot bounds")
	}
	out := TableSnapshot{Version:1,Slug:slug,Rows:[]entity.DataTableRow{}}
	opts := &sql.TxOptions{}
	if f.db.Dialector.Name() == "postgres" {
		opts.Isolation = sql.LevelRepeatableRead
		opts.ReadOnly = true
	}
	err := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var scope ChangeScope
		err := tx.First(&scope,"slug = ?",slug).Error
		if errors.Is(err,gorm.ErrRecordNotFound) || (err == nil && !scope.Enabled) { return ErrChangeFeedDisabled }
		if err != nil { return err }
		out.Watermark = scope.Sequence
		var table entity.DataTable
		err = tx.First(&table,"slug = ?",slug).Error
		if errors.Is(err,gorm.ErrRecordNotFound) {
			var n int64
			if err := tx.Model(&entity.DataTableRow{}).Where("table_slug = ?",slug).Count(&n).Error; err != nil { return err }
			if n != 0 { return fmt.Errorf("change feed: orphaned rows") }
			return nil
		}
		if err != nil { return err }
		if !json.Valid([]byte(table.SchemaJSON)) || (table.AccessJSON != "" && !json.Valid([]byte(table.AccessJSON))) { return fmt.Errorf("change feed: invalid stored schema") }
		out.Table = &table
		header,err := json.Marshal(out)
		if err != nil { return err }
		remaining := maxBytes-len(header)
		if remaining < 0 { return ErrChangeSnapshotTooLarge }
		rows,err := tx.Model(&entity.DataTableRow{}).Where("table_slug = ?",slug).Order("id ASC").Limit(maxRows+1).Rows()
		if err != nil { return err }
		defer rows.Close()
		for rows.Next() {
			if len(out.Rows) == maxRows { return ErrChangeSnapshotTooLarge }
			var row entity.DataTableRow
			if err := tx.ScanRows(rows,&row); err != nil { return err }
			if !json.Valid([]byte(row.Data)) { return fmt.Errorf("change feed: invalid stored row") }
			b,err := json.Marshal(row)
			if err != nil { return err }
			remaining -= len(b)
			if len(out.Rows)>0 { remaining-- }
			if remaining < 0 { return ErrChangeSnapshotTooLarge }
			out.Rows=append(out.Rows,row)
		}
		return rows.Err()
	},opts)
	if err != nil { return TableSnapshot{},err }
	encoded,err := json.Marshal(out)
	if err != nil { return TableSnapshot{},err }
	if len(encoded)>maxBytes { return TableSnapshot{},ErrChangeSnapshotTooLarge }
	return out,nil
}
