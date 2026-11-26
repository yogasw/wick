package datatable

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"gorm.io/gorm"
)

var (
	ErrChangeFeedDisabled = errors.New("change feed: scope disabled")
	ErrChangeSnapshotRequired = errors.New("change feed: snapshot required")
	ErrChangeCursorConflict = errors.New("change feed: cursor conflict")
)

// ChangeCursor is a local delivery checkpoint, not a peer authorization.
// No cursor can imply permission to read table data or to delete history.
type ChangeCursor struct {
	Slug string `gorm:"primaryKey;size:80"`
	Peer string `gorm:"primaryKey;size:64"`
	Sequence int64 `gorm:"not null"`
}

func (ChangeCursor) TableName() string { return "wick_dt_change_cursors" }

// withScope serializes cursor operations with the trigger sequence allocator.
// SQLite takes its writer lock; PostgreSQL locks the existing scope row.
// A busy/conflicting transaction fails rather than acknowledging stale data.
func (f *ChangeFeed) withScope(slug, peer string, fn func(*gorm.DB, ChangeScope, int64, *ChangeCursor) error) error {
	if err := parse.ValidateID(slug); err != nil { return err }
	key, err := hex.DecodeString(peer)
	if err != nil || len(key) != 32 || hex.EncodeToString(key) != peer {
		return fmt.Errorf("change feed: invalid peer ID")
	}
	return f.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE wick_dt_change_scopes SET sequence = sequence WHERE slug = ? AND enabled = TRUE", slug)
		if res.Error != nil { return res.Error }
		if res.RowsAffected != 1 { return ErrChangeFeedDisabled }
		var scope ChangeScope
		if err := tx.First(&scope, "slug = ?", slug).Error; err != nil { return err }
		var boundary ChangeEvent
		err := tx.Where("slug = ? AND kind = ?", slug, "snapshot_required").Order("sequence DESC").First(&boundary).Error
		if err != nil { return fmt.Errorf("change feed: missing snapshot boundary: %w", err) }
		var cursor ChangeCursor
		err = tx.First(&cursor, "slug = ? AND peer = ?", slug, peer).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fn(tx, scope, boundary.Sequence, nil)
		}
		if err != nil { return err }
		return fn(tx, scope, boundary.Sequence, &cursor)
	})
}

// ConfirmSnapshot records an explicitly acknowledged consistent snapshot.
// Trusted caller MUST independently verify authorization and durable receipt
// of the snapshot at watermark. This does not create, export or verify it.
// Re-enabling a scope establishes a new boundary that invalidates old ACKs.
func (f *ChangeFeed) ConfirmSnapshot(slug, peer string, watermark int64) error {
	return f.withScope(slug, peer, func(tx *gorm.DB, scope ChangeScope, boundary int64, cursor *ChangeCursor) error {
		if watermark < boundary || watermark > scope.Sequence { return ErrChangeSnapshotRequired }
		if cursor != nil && watermark < cursor.Sequence { return ErrChangeCursorConflict }
		if cursor == nil {
			return tx.Create(&ChangeCursor{Slug:slug, Peer:peer, Sequence:watermark}).Error
		}
		return tx.Model(cursor).Update("sequence", watermark).Error
	})
}

// ReadForPeer returns the committed cursor and the next bounded contiguous
// metadata page. Disabled scopes, missing snapshots and any history gaps
// fail closed. This is a trusted local API; never expose directly to peers.
func (f *ChangeFeed) ReadForPeer(slug, peer string, limit int) (int64, []ChangeEvent, error) {
	if limit < 1 || limit > 1000 { return 0,nil,fmt.Errorf("change feed: invalid page limit") }
	var after int64
	var events []ChangeEvent
	err := f.withScope(slug, peer, func(tx *gorm.DB, scope ChangeScope, boundary int64, cursor *ChangeCursor) error {
		if cursor == nil || cursor.Sequence < boundary { return ErrChangeSnapshotRequired }
		after = cursor.Sequence
		if after > scope.Sequence { return ErrChangeCursorConflict }
		if err := tx.Where("slug = ? AND sequence > ?",slug,after).Order("sequence ASC").Limit(limit).Find(&events).Error; err != nil { return err }
		for i,e := range events {
			if e.Sequence != after+int64(i)+1 || e.Kind == "snapshot_required" { return ErrChangeSnapshotRequired }
		}
		if len(events) == 0 && after < scope.Sequence { return ErrChangeSnapshotRequired }
		return nil
	})
	if err != nil { return 0,nil,err }
	return after,events,nil
}

// AcknowledgePage advances only from the expected cursor over an existing,
// contiguous bounded page. Duplicate ACKs for that same committed endpoint
// are harmless. The trusted caller must verify durable remote apply first;
// a network response or peer claim alone is not sufficient.
func (f *ChangeFeed) AcknowledgePage(slug, peer string, after, through int64) error {
	if after < 0 || through <= after || through-after > 1000 { return fmt.Errorf("change feed: invalid ACK range") }
	return f.withScope(slug, peer, func(tx *gorm.DB, scope ChangeScope, boundary int64, cursor *ChangeCursor) error {
		if cursor == nil || cursor.Sequence < boundary || after < boundary { return ErrChangeSnapshotRequired }
		if through > scope.Sequence { return ErrChangeCursorConflict }
		if cursor.Sequence == through { return nil }
		if cursor.Sequence != after { return ErrChangeCursorConflict }
		var events []ChangeEvent
		if err := tx.Where("slug = ? AND sequence > ? AND sequence <= ?",slug,after,through).Order("sequence ASC").Find(&events).Error; err != nil { return err }
		if int64(len(events)) != through-after { return ErrChangeSnapshotRequired }
		for i,e := range events {
			if e.Sequence != after+int64(i)+1 || e.Kind == "snapshot_required" { return ErrChangeSnapshotRequired }
		}
		return tx.Model(cursor).Update("sequence",through).Error
	})
}
