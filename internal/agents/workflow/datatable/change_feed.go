package datatable

import (
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"gorm.io/gorm"
)

// ChangeScope is an explicitly enabled local change stream. Sequence numbers
// are serialized per slug by the database, not allocated by an independent
// sequence that could commit out of order. Never reset a stream's counter.
type ChangeScope struct {
	Slug string `gorm:"primaryKey;size:80"`
	Sequence int64 `gorm:"not null;default:0"`
	Enabled bool `gorm:"not null"`
}
func (ChangeScope) TableName() string { return "wick_dt_change_scopes" }

// ChangeEvent contains only mutation metadata, never row contents or ACLs.
// RowID is nil for table/schema changes. IDs are local, not Mesh global IDs.
type ChangeEvent struct {
	Slug string `gorm:"primaryKey;size:80"`
	Sequence int64 `gorm:"primaryKey;autoIncrement:false"`
	Version int `gorm:"not null"`
	Kind string `gorm:"not null;size:24"`
	RowID *int64
}
func (ChangeEvent) TableName() string { return "wick_dt_change_events" }

// ChangeFeed is a trusted in-process API, not a remote or caller-authorized
// endpoint. Its consumers must perform resource ACL checks separately.
// Installation is explicit and must occur after the datatable schema migration.
type ChangeFeed struct { db *gorm.DB }

type changeTrigger struct {
	name, table, operation, scope, row, kind string
}

var changeTriggers = []changeTrigger{
	{"row_insert","wick_data_table_rows","INSERT","NEW.table_slug","NEW.id","row_insert"},
	{"row_update","wick_data_table_rows","UPDATE","NEW.table_slug","NEW.id","row_update"},
	{"row_delete","wick_data_table_rows","DELETE","OLD.table_slug","OLD.id","row_delete"},
	{"table_insert","wick_data_tables","INSERT","NEW.slug","NULL","table_insert"},
	{"table_update","wick_data_tables","UPDATE","NEW.slug","NULL","table_update"},
	{"table_delete","wick_data_tables","DELETE","OLD.slug","NULL","table_delete"},
}

// InstallChangeFeed installs transactional triggers for SQLite and PostgreSQL.
// Reinstallation preserves stream state and is safe after a process restart.
// Normal NewPg users do not install triggers or collect mutation metadata.
func InstallChangeFeed(db *gorm.DB) (*ChangeFeed, error) {
	if db == nil || db.Dialector == nil { return nil, fmt.Errorf("change feed: missing database") }
	dialect := db.Dialector.Name()
	if dialect != "sqlite" && dialect != "postgres" { return nil, fmt.Errorf("change feed: unsupported dialect %q", dialect) }
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(&ChangeScope{}, &ChangeEvent{}); err != nil { return err }
		for _, tr := range changeTriggers {
			name := "wick_dt_feed_" + tr.name
			// The allocator updates only next_row_id; that is not a schema
			// mutation. Access/owner changes invalidate metadata too.
			predicate := ""
			if tr.name == "table_update" {
				cols := []string{"name","description","mode","schema_json","access_json","created_by"}
				for _, col := range cols {
					if predicate != "" { predicate += " OR " }
					if dialect == "sqlite" { predicate += "OLD."+col+" IS NOT NEW."+col } else { predicate += "OLD."+col+" IS DISTINCT FROM NEW."+col }
				}
			}
			if dialect == "sqlite" {
				if err := tx.Exec("DROP TRIGGER IF EXISTS "+name).Error; err != nil { return err }
				when := ""
				if predicate != "" { when = " WHEN "+predicate }
				sql := fmt.Sprintf(`CREATE TRIGGER %s AFTER %s ON %s%s BEGIN
UPDATE wick_dt_change_scopes SET sequence = sequence + 1 WHERE slug = %s AND enabled = TRUE;
INSERT INTO wick_dt_change_events (slug,sequence,version,kind,row_id)
SELECT slug,sequence,1,'%s',%s FROM wick_dt_change_scopes WHERE slug = %s AND enabled = TRUE;
END`, name,tr.operation,tr.table,when,tr.scope,tr.kind,tr.row,tr.scope)
				if err := tx.Exec(sql).Error; err != nil { return err }
			} else {
				if err := tx.Exec("DROP TRIGGER IF EXISTS "+name+" ON "+tr.table).Error; err != nil { return err }
				when := ""
				if predicate != "" { when = " WHEN ("+predicate+")" }
				sql := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $feed$
DECLARE next_seq bigint;
BEGIN
UPDATE wick_dt_change_scopes SET sequence = sequence + 1 WHERE slug = %s AND enabled = TRUE RETURNING sequence INTO next_seq;
IF FOUND THEN
INSERT INTO wick_dt_change_events (slug,sequence,version,kind,row_id) VALUES (%s,next_seq,1,'%s',%s);
END IF;
RETURN NULL;
END;
$feed$`,name,tr.scope,tr.scope,tr.kind,tr.row)
				if err := tx.Exec(sql).Error; err != nil { return err }
				if err := tx.Exec(fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON %s FOR EACH ROW%s EXECUTE FUNCTION %s()",name,tr.operation,tr.table,when,name)).Error; err != nil { return err }
			}
		}
		return nil
	})
	if err != nil { return nil, err }
	return &ChangeFeed{db:db}, nil
}

// SetEnabled is an explicit local opt-in. Disabling preserves existing events
// and the counter. Enabling after a gap requires a fresh snapshot by the
// eventual consumer; this API does not assert replica readiness.
func (f *ChangeFeed) SetEnabled(slug string, enabled bool) error {
	if err := parse.ValidateID(slug); err != nil { return err }
	return f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO wick_dt_change_scopes (slug,sequence,enabled) VALUES (?,0,FALSE)
ON CONFLICT (slug) DO NOTHING`,slug).Error; err != nil { return err }
		if !enabled {
			return tx.Exec("UPDATE wick_dt_change_scopes SET enabled = FALSE WHERE slug = ?",slug).Error
		}
		res := tx.Exec("UPDATE wick_dt_change_scopes SET enabled = TRUE, sequence = sequence + 1 WHERE slug = ? AND enabled = FALSE",slug)
		if res.Error != nil { return res.Error }
		if res.RowsAffected == 0 { return nil }
		// Explicit durable boundary: a consumer cannot mistake a stream
		// resumed after an unobserved interval for continuous history.
		return tx.Exec(`INSERT INTO wick_dt_change_events (slug,sequence,version,kind,row_id)
SELECT slug,sequence,1,'snapshot_required',NULL FROM wick_dt_change_scopes WHERE slug = ?`,slug).Error
	})
}

// Read returns a bounded ordered page for exactly one local scope. Delivery,
// remote authorization, durable per-peer ACK and retention belong to the
// replication layer and are intentionally not implied by this method.
func (f *ChangeFeed) Read(slug string, after int64, limit int) ([]ChangeEvent,error) {
	if err := parse.ValidateID(slug); err != nil { return nil,err }
	if after < 0 || limit < 1 || limit > 1000 { return nil,fmt.Errorf("change feed: invalid page") }
	var events []ChangeEvent
	err := f.db.Where("slug = ? AND sequence > ?",slug,after).Order("sequence ASC").Limit(limit).Find(&events).Error
	return events,err
}
