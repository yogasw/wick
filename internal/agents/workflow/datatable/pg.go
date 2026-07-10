// Package datatable — Postgres backend, shared JSONB rows model.
//
// Two physical tables hold everything:
//
//   - wick_data_tables       : one row per logical table, schema in JSONB
//   - wick_data_table_rows   : one row per data row, keyed by table_slug,
//     data column is JSONB keyed by column id
//
// Renames touch only the schema row, never the data; column ids are
// monotonic per table (Schema.NextColID) so dropped column ids never
// reappear and orphan keys in legacy data are safely ignored on read.
package datatable

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	"github.com/yogasw/wick/internal/entity"
)

// PgService is the GORM-backed Service. Write-through, no in-memory
// cache — every read hits Postgres so multi-instance deploys and
// process restarts see the same state.
type PgService struct{ db *gorm.DB }

// NewPg wires a Postgres-backed service. Caller is responsible for
// running AutoMigrate on entity.DataTable + entity.DataTableRow.
func NewPg(db *gorm.DB) *PgService { return &PgService{db: db} }

// ── helpers ─────────────────────────────────────────────────────────

var safeIdent = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// quoteIdent wraps a Postgres identifier in double quotes after
// validating its shape. Used only for index names (column ids/slugs
// flow into bind args, not raw SQL).
func quoteIdent(name string) (string, error) {
	if !safeIdent.MatchString(name) {
		return "", fmt.Errorf("unsafe identifier %q", name)
	}
	return `"` + name + `"`, nil
}

// pgIndexCast returns the Postgres cast expression for a column type.
// JSON path extract always yields text; numeric/timestamp filters need
// the matching cast on both the index expression and the query.
func pgIndexCast(t string) string {
	switch t {
	case "int":
		return "::bigint"
	case "float":
		return "::double precision"
	case "bool":
		return "::boolean"
	case "timestamp":
		return "::timestamptz"
	}
	return ""
}

func encodeSchemaJSON(sc Schema) (string, error) {
	b, err := json.Marshal(sc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeSchemaJSON(raw string) (Schema, error) {
	var sc Schema
	if strings.TrimSpace(raw) == "" {
		return sc, nil
	}
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		return sc, err
	}
	return sc, nil
}

func encodeAccessJSON(a Access) (string, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// loadMeta reads one wick_data_tables row by slug. Returns a
// "not registered" error when missing so callers don't have to
// import gorm just to recognise the case.
func (s *PgService) loadMeta(tx *gorm.DB, slug string) (entity.DataTable, error) {
	var m entity.DataTable
	q := tx.Where("slug = ?", slug)
	if err := q.First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return m, fmt.Errorf("data table %q not registered", slug)
		}
		return m, err
	}
	return m, nil
}

// metaToSchema decodes the JSONB schema body and applies the
// metadata fields wick_data_tables manages as columns (slug, mode,
// timestamps).
func metaToSchema(m entity.DataTable) (Schema, error) {
	sc, err := decodeSchemaJSON(m.SchemaJSON)
	if err != nil {
		return Schema{}, err
	}
	sc.Slug = m.Slug
	sc.Name = m.Name
	sc.Description = m.Description
	sc.UserID = m.CreatedBy
	if m.Mode != "" {
		sc.Mode = m.Mode
	}
	if sc.Mode == "" {
		sc.Mode = ModeStrict
	}
	sc.PrimaryKey = []string{ColID}
	sc.CreatedAt = m.CreatedAt
	sc.UpdatedAt = m.UpdatedAt
	// Ensure system columns are present so the UI can render the
	// reserved trio even if the persisted schema_json was minted by an
	// older code path that skipped them.
	sc.Columns = mergeSystemColumns(sc.Columns)
	return sc, nil
}

// ── Service iface ───────────────────────────────────────────────────

// CreateTable persists a new table's metadata. No DDL — rows live in
// the shared wick_data_table_rows table.
func (s *PgService) CreateTable(sc Schema) error {
	if err := parse.ValidateID(sc.Slug); err != nil {
		return err
	}
	if sc.Mode == "" {
		sc.Mode = ModeStrict
	}
	if sc.Name == "" {
		sc.Name = sc.Slug
	}
	// Assign ids to any user columns missing one (callers can pass a
	// schema with named-but-unassigned cols and let the service mint).
	for i := range sc.Columns {
		c := &sc.Columns[i]
		if c.System {
			continue
		}
		if c.ID == "" {
			c.ID = NextColumnID(&sc)
		}
	}
	sc.Columns = mergeSystemColumns(sc.Columns)
	sc.PrimaryKey = []string{ColID}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing entity.DataTable
		if err := tx.Where("slug = ?", sc.Slug).First(&existing).Error; err == nil {
			return fmt.Errorf("data table %q already exists", sc.Slug)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		schemaJSON, err := encodeSchemaJSON(sc)
		if err != nil {
			return err
		}
		accessJSON, err := encodeAccessJSON(sc.Access)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		row := entity.DataTable{
			Slug:        sc.Slug,
			Name:        sc.Name,
			Description: sc.Description,
			Mode:        sc.Mode,
			SchemaJSON:  schemaJSON,
			AccessJSON:  accessJSON,
			NextRowID:   0,
			CreatedBy:   sc.UserID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return s.applyIndexes(tx, sc.Slug, sc.Columns, nil)
	})
}

// DropTable removes all data rows + the metadata row + every
// per-table index. Atomic.
func (s *PgService) DropTable(slug string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.loadMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		if err := s.dropAllIndexes(tx, slug, sc.Columns); err != nil {
			return err
		}
		if err := tx.Where("table_slug = ?", slug).Delete(&entity.DataTableRow{}).Error; err != nil {
			return err
		}
		return tx.Where("slug = ?", slug).Delete(&entity.DataTable{}).Error
	})
}

// ListTables returns every registered slug, ordered by creation time.
func (s *PgService) ListTables() []string {
	var slugs []string
	s.db.Model(&entity.DataTable{}).Order("created_at ASC").Pluck("slug", &slugs)
	return slugs
}

// LoadSchema returns the registered schema for a slug.
func (s *PgService) LoadSchema(slug string) (Schema, error) {
	m, err := s.loadMeta(s.db, slug)
	if err != nil {
		return Schema{}, err
	}
	return metaToSchema(m)
}

// SaveSchema replaces the schema. Adds + drops are diffed against the
// previous schema so dropped columns also strip the matching JSONB
// key from every row (Postgres `data - 'cN'`) and drop the partial
// functional index when one exists. Renames are NOT detected here —
// callers should use RenameColumn for that, since SaveSchema cannot
// tell a rename apart from a drop+add.
func (s *PgService) SaveSchema(sc Schema) error {
	if err := parse.ValidateID(sc.Slug); err != nil {
		return err
	}
	if sc.Mode == "" {
		sc.Mode = ModeStrict
	}
	if sc.Name == "" {
		sc.Name = sc.Slug
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.loadMeta(tx, sc.Slug)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err != nil && strings.Contains(err.Error(), "not registered")) {
			// Promote to create when slug is new.
			return s.createInTx(tx, sc)
		}
		if err != nil {
			return err
		}
		prev, err := metaToSchema(m)
		if err != nil {
			return err
		}

		// Assign ids to any newly-introduced user columns.
		prevByID := map[string]Column{}
		for _, c := range prev.Columns {
			if !c.System {
				prevByID[c.ID] = c
			}
		}
		// Make sure incoming NextColID is at least as high as prev so
		// fresh columns don't accidentally reuse a burned id.
		if sc.NextColID < prev.NextColID {
			sc.NextColID = prev.NextColID
		}
		nextByID := map[string]Column{}
		for i := range sc.Columns {
			c := &sc.Columns[i]
			if c.System {
				continue
			}
			if c.ID == "" {
				c.ID = NextColumnID(&sc)
			}
			nextByID[c.ID] = *c
		}
		sc.Columns = mergeSystemColumns(sc.Columns)
		sc.PrimaryKey = []string{ColID}

		// Compute dropped column ids — entries present before but not
		// in the new schema. Strip JSONB keys + drop their indexes.
		var dropped []Column
		for id, c := range prevByID {
			if _, kept := nextByID[id]; !kept {
				dropped = append(dropped, c)
			}
		}
		if len(dropped) > 0 {
			if err := s.stripDataKeys(tx, sc.Slug, dropped); err != nil {
				return err
			}
		}

		schemaJSON, err := encodeSchemaJSON(sc)
		if err != nil {
			return err
		}
		accessJSON, err := encodeAccessJSON(sc.Access)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"name":        sc.Name,
			"description": sc.Description,
			"mode":        sc.Mode,
			"schema_json": schemaJSON,
			"access_json": accessJSON,
			"updated_at":  time.Now().UTC(),
		}
		if err := tx.Model(&entity.DataTable{}).Where("slug = ?", sc.Slug).Updates(updates).Error; err != nil {
			return err
		}
		return s.applyIndexes(tx, sc.Slug, sc.Columns, prev.Columns)
	})
}

func (s *PgService) createInTx(tx *gorm.DB, sc Schema) error {
	for i := range sc.Columns {
		c := &sc.Columns[i]
		if c.System {
			continue
		}
		if c.ID == "" {
			c.ID = NextColumnID(&sc)
		}
	}
	sc.Columns = mergeSystemColumns(sc.Columns)
	sc.PrimaryKey = []string{ColID}
	schemaJSON, err := encodeSchemaJSON(sc)
	if err != nil {
		return err
	}
	accessJSON, err := encodeAccessJSON(sc.Access)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	row := entity.DataTable{
		Slug: sc.Slug, Name: sc.Name, Description: sc.Description,
		Mode: sc.Mode, SchemaJSON: schemaJSON, AccessJSON: accessJSON,
		NextRowID: 0, CreatedBy: sc.UserID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	return s.applyIndexes(tx, sc.Slug, sc.Columns, nil)
}

// RenameColumn changes the human-facing label of a user column. The
// storage id stays the same so no data row is touched.
func (s *PgService) RenameColumn(slug, from, to string) error {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return fmt.Errorf("rename column: from and to required")
	}
	if from == to {
		return nil
	}
	if IsSystemColumn(from) {
		return fmt.Errorf("cannot rename system column %q", from)
	}
	if IsSystemColumn(to) {
		return fmt.Errorf("cannot use reserved name %q", to)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.loadMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		var srcIdx = -1
		for i, c := range sc.Columns {
			if c.System {
				continue
			}
			if c.Name == to {
				return fmt.Errorf("column %q already exists", to)
			}
			if c.Name == from {
				srcIdx = i
			}
		}
		if srcIdx == -1 {
			return fmt.Errorf("column %q not found", from)
		}
		sc.Columns[srcIdx].Name = to
		schemaJSON, err := encodeSchemaJSON(sc)
		if err != nil {
			return err
		}
		return tx.Model(&entity.DataTable{}).Where("slug = ?", slug).Updates(map[string]any{
			"schema_json": schemaJSON,
			"updated_at":  time.Now().UTC(),
		}).Error
	})
}

// DropColumn removes a user column. Schema entry goes first; the
// matching JSONB key is stripped from every row in the same TX, and
// the partial functional index (if any) is dropped.
func (s *PgService) DropColumn(slug, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("drop column: name required")
	}
	if IsSystemColumn(name) {
		return fmt.Errorf("cannot drop system column %q", name)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.loadMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		var target Column
		idx := -1
		for i, c := range sc.Columns {
			if c.System {
				continue
			}
			if c.Name == name {
				idx = i
				target = c
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("column %q not found", name)
		}
		sc.Columns = append(sc.Columns[:idx], sc.Columns[idx+1:]...)
		schemaJSON, err := encodeSchemaJSON(sc)
		if err != nil {
			return err
		}
		if err := s.stripDataKeys(tx, slug, []Column{target}); err != nil {
			return err
		}
		return tx.Model(&entity.DataTable{}).Where("slug = ?", slug).Updates(map[string]any{
			"schema_json": schemaJSON,
			"updated_at":  time.Now().UTC(),
		}).Error
	})
}

// ── row ops ──────────────────────────────────────────────────────────

// Insert appends a new row. id is allocated from the per-slug
// monotonic counter inside the TX so concurrent inserts never collide.
func (s *PgService) Insert(slug string, row map[string]any) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.lockMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		stripSystemKeys(row)
		cleaned, err := normalizeRow(sc, row)
		if err != nil {
			return err
		}
		idmap := BuildIDMap(sc)
		encoded, err := idmap.Encode(cleaned, sc.Mode)
		if err != nil {
			return err
		}
		nextID, err := s.bumpNextRowID(tx, slug, m.NextRowID)
		if err != nil {
			return err
		}
		data, err := json.Marshal(encoded)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Create(&entity.DataTableRow{
			TableSlug: slug,
			ID:        nextID,
			Data:      string(data),
			CreatedAt: now,
			UpdatedAt: now,
		}).Error
	})
}

// Upsert insert-or-updates by id. When row["id"] hits an existing
// row, fields are merged via JSONB concat; otherwise a fresh id is
// minted and the row is inserted.
func (s *PgService) Upsert(slug string, row map[string]any) (string, error) {
	var action string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.lockMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		targetID, hasID := coerceInt64(row[ColID])
		delete(row, ColID)
		delete(row, ColCreatedAt)
		delete(row, ColUpdatedAt)
		cleaned, err := normalizeRow(sc, row)
		if err != nil {
			return err
		}
		idmap := BuildIDMap(sc)
		encoded, err := idmap.Encode(cleaned, sc.Mode)
		if err != nil {
			return err
		}
		data, err := json.Marshal(encoded)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if hasID {
			res := tx.Model(&entity.DataTableRow{}).
				Where("table_slug = ? AND id = ?", slug, targetID).
				Updates(map[string]any{
					"data":       gorm.Expr(`COALESCE("data", '{}'::jsonb) || ?::jsonb`, string(data)),
					"updated_at": now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 {
				action = "update"
				return nil
			}
		}
		// Fall through to insert (with the requested id when supplied).
		var newID int64
		if hasID {
			newID = targetID
			if newID > m.NextRowID {
				if err := tx.Model(&entity.DataTable{}).Where("slug = ?", slug).
					Update("next_row_id", newID).Error; err != nil {
					return err
				}
			}
		} else {
			id, err := s.bumpNextRowID(tx, slug, m.NextRowID)
			if err != nil {
				return err
			}
			newID = id
		}
		if err := tx.Create(&entity.DataTableRow{
			TableSlug: slug, ID: newID, Data: string(data),
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			return err
		}
		action = "insert"
		return nil
	})
	return action, err
}

// Get returns the first row matching the caller-supplied filter
// (translated to id keys). Rows are decoded back to name keys before
// return.
func (s *PgService) Get(slug string, key map[string]any) (map[string]any, bool, error) {
	rows, err := s.queryNameKeyed(slug, key, nil, nil, 1, 0)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}

// Exists is Get with a boolean view.
func (s *PgService) Exists(slug string, where map[string]any) (bool, error) {
	_, found, err := s.Get(slug, where)
	return found, err
}

// Count returns the number of rows matching the where map.
func (s *PgService) Count(slug string, where map[string]any) (int, error) {
	return s.countInternal(slug, where, nil)
}

// CountConditions counts rows matching every Condition.
func (s *PgService) CountConditions(slug string, conds []Condition) (int, error) {
	return s.countInternal(slug, nil, conds)
}

// Query returns rows matching the where filter, sorted by order.
func (s *PgService) Query(slug string, where map[string]any, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	return s.queryNameKeyed(slug, where, nil, order, limit, offset)
}

// QueryConditions returns rows matching every Condition.
func (s *PgService) QueryConditions(slug string, conds []Condition, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	return s.queryNameKeyed(slug, nil, conds, order, limit, offset)
}

// Delete removes rows matching the where filter.
func (s *PgService) Delete(slug string, where map[string]any) (int, error) {
	return s.deleteInternal(slug, where, nil)
}

// DeleteConditions removes rows matching every Condition.
func (s *PgService) DeleteConditions(slug string, conds []Condition) (int, error) {
	return s.deleteInternal(slug, nil, conds)
}

// ── shared query plumbing ───────────────────────────────────────────

// where holds the rendered WHERE fragment + bind args for a query.
type where struct {
	clause string
	args   []any
}

// buildWhere translates either an equality map or a condition list
// into a parameterised SQL WHERE fragment. table_slug is always
// pinned by the caller (added in queryNameKeyed/countInternal) — this
// helper handles only the user-supplied filter.
func (s *PgService) buildWhere(idmap IDMap, eq map[string]any, conds []Condition) (where, error) {
	w := where{}
	if len(conds) > 0 {
		parts := []string{}
		for _, c := range conds {
			cid, ok := idmap.NameToID[c.Column]
			if !ok {
				if !IsSystemColumn(c.Column) {
					return w, fmt.Errorf("unknown column %q", c.Column)
				}
				// system columns map to literal table columns
				expr, args, err := systemColumnPredicate(c)
				if err != nil {
					return w, err
				}
				parts = append(parts, expr)
				w.args = append(w.args, args...)
				continue
			}
			cast := pgIndexCast(idmap.Cols[cid].Type)
			expr, args, err := jsonbPredicate(cid, cast, c)
			if err != nil {
				return w, err
			}
			parts = append(parts, expr)
			w.args = append(w.args, args...)
		}
		if len(parts) > 0 {
			w.clause = strings.Join(parts, " AND ")
		}
		return w, nil
	}
	parts := []string{}
	for name, v := range eq {
		if IsSystemColumn(name) {
			parts = append(parts, fmt.Sprintf("%q = ?", name))
			w.args = append(w.args, v)
			continue
		}
		cid, ok := idmap.NameToID[name]
		if !ok {
			return w, fmt.Errorf("unknown column %q", name)
		}
		parts = append(parts, fmt.Sprintf(`(data->>'%s') = ?`, cid))
		w.args = append(w.args, fmt.Sprintf("%v", v))
	}
	if len(parts) > 0 {
		w.clause = strings.Join(parts, " AND ")
	}
	return w, nil
}

// jsonbPredicate renders one Condition against `data->>'<cid>'`.
func jsonbPredicate(cid, cast string, c Condition) (string, []any, error) {
	col := fmt.Sprintf(`(data->>'%s')%s`, cid, cast)
	switch c.Op {
	case "", OpEquals:
		return col + " = ?", []any{c.Value}, nil
	case OpNotEquals:
		return col + " <> ?", []any{c.Value}, nil
	case OpGT:
		return col + " > ?", []any{c.Value}, nil
	case OpGTE:
		return col + " >= ?", []any{c.Value}, nil
	case OpLT:
		return col + " < ?", []any{c.Value}, nil
	case OpLTE:
		return col + " <= ?", []any{c.Value}, nil
	case OpContains:
		return fmt.Sprintf(`(data->>'%s') ILIKE ?`, cid), []any{"%" + fmt.Sprintf("%v", c.Value) + "%"}, nil
	case OpIn:
		vals, ok := c.Value.([]any)
		if !ok {
			if ss, ok2 := c.Value.([]string); ok2 {
				for _, v := range ss {
					vals = append(vals, v)
				}
			}
		}
		if len(vals) == 0 {
			return "FALSE", nil, nil
		}
		placeholders := make([]string, len(vals))
		for i := range vals {
			placeholders[i] = "?"
		}
		return col + " IN (" + strings.Join(placeholders, ", ") + ")", vals, nil
	case OpIsEmpty:
		return fmt.Sprintf(`((data ? '%s') IS FALSE OR data->>'%s' IS NULL OR data->>'%s' = '')`, cid, cid, cid), nil, nil
	case OpIsNotEmpty:
		return fmt.Sprintf(`(data ? '%s') AND data->>'%s' IS NOT NULL AND data->>'%s' <> ''`, cid, cid, cid), nil, nil
	}
	return "", nil, fmt.Errorf("unknown op %q", c.Op)
}

// systemColumnPredicate renders a Condition against a system column
// (id / created_at / updated_at) which lives as a real table column,
// not in JSONB.
func systemColumnPredicate(c Condition) (string, []any, error) {
	col := fmt.Sprintf("%q", c.Column)
	switch c.Op {
	case "", OpEquals:
		return col + " = ?", []any{c.Value}, nil
	case OpNotEquals:
		return col + " <> ?", []any{c.Value}, nil
	case OpGT:
		return col + " > ?", []any{c.Value}, nil
	case OpGTE:
		return col + " >= ?", []any{c.Value}, nil
	case OpLT:
		return col + " < ?", []any{c.Value}, nil
	case OpLTE:
		return col + " <= ?", []any{c.Value}, nil
	}
	return "", nil, fmt.Errorf("op %q not supported on system column %q", c.Op, c.Column)
}

// orderBy renders ORDER BY for a request. Falls back to `id ASC` so
// pagination is deterministic.
func orderBy(idmap IDMap, order []workflow.DataTableOrder) string {
	if len(order) == 0 {
		return ` ORDER BY "id" ASC`
	}
	parts := []string{}
	for _, o := range order {
		dir := "ASC"
		if strings.EqualFold(o.Direction, "desc") {
			dir = "DESC"
		}
		if IsSystemColumn(o.Column) {
			parts = append(parts, fmt.Sprintf("%q %s", o.Column, dir))
			continue
		}
		cid, ok := idmap.NameToID[o.Column]
		if !ok {
			continue
		}
		cast := pgIndexCast(idmap.Cols[cid].Type)
		parts = append(parts, fmt.Sprintf(`(data->>'%s')%s %s`, cid, cast, dir))
	}
	if len(parts) == 0 {
		return ` ORDER BY "id" ASC`
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

// queryNameKeyed runs a SELECT with the supplied filter, decodes each
// row back to caller-facing names, and surfaces system columns
// (id/created_at/updated_at) alongside user fields.
func (s *PgService) queryNameKeyed(slug string, eq map[string]any, conds []Condition, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	m, err := s.loadMeta(s.db, slug)
	if err != nil {
		return nil, err
	}
	sc, err := metaToSchema(m)
	if err != nil {
		return nil, err
	}
	idmap := BuildIDMap(sc)
	w, err := s.buildWhere(idmap, eq, conds)
	if err != nil {
		return nil, err
	}
	args := []any{slug}
	clause := `"table_slug" = ?`
	if w.clause != "" {
		clause += " AND " + w.clause
		args = append(args, w.args...)
	}
	sqlStr := `SELECT "id", "data", "created_at", "updated_at" FROM wick_data_table_rows WHERE ` + clause + orderBy(idmap, order)
	if limit > 0 {
		sqlStr += fmt.Sprintf(" LIMIT %d", limit)
	}
	if offset > 0 {
		sqlStr += fmt.Sprintf(" OFFSET %d", offset)
	}
	rows, err := s.db.WithContext(context.Background()).Raw(sqlStr, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var data string
		var createdAt, updatedAt sql.NullTime
		if err := rows.Scan(&id, &data, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		var stored map[string]any
		if data != "" {
			if err := json.Unmarshal([]byte(data), &stored); err != nil {
				return nil, err
			}
		}
		row := idmap.Decode(stored)
		row[ColID] = id
		if createdAt.Valid {
			row[ColCreatedAt] = createdAt.Time
		}
		if updatedAt.Valid {
			row[ColUpdatedAt] = updatedAt.Time
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *PgService) countInternal(slug string, eq map[string]any, conds []Condition) (int, error) {
	m, err := s.loadMeta(s.db, slug)
	if err != nil {
		return 0, err
	}
	sc, err := metaToSchema(m)
	if err != nil {
		return 0, err
	}
	idmap := BuildIDMap(sc)
	w, err := s.buildWhere(idmap, eq, conds)
	if err != nil {
		return 0, err
	}
	args := []any{slug}
	clause := `"table_slug" = ?`
	if w.clause != "" {
		clause += " AND " + w.clause
		args = append(args, w.args...)
	}
	var n int64
	row := s.db.Raw("SELECT COUNT(*) FROM wick_data_table_rows WHERE "+clause, args...).Row()
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *PgService) deleteInternal(slug string, eq map[string]any, conds []Condition) (int, error) {
	var n int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		m, err := s.loadMeta(tx, slug)
		if err != nil {
			return err
		}
		sc, err := metaToSchema(m)
		if err != nil {
			return err
		}
		idmap := BuildIDMap(sc)
		w, err := s.buildWhere(idmap, eq, conds)
		if err != nil {
			return err
		}
		args := []any{slug}
		clause := `"table_slug" = ?`
		if w.clause != "" {
			clause += " AND " + w.clause
			args = append(args, w.args...)
		}
		res := tx.Exec("DELETE FROM wick_data_table_rows WHERE "+clause, args...)
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		return nil
	})
	return int(n), err
}

// ── id allocator + data mutation helpers ────────────────────────────

// lockMeta loads the metadata row under SELECT FOR UPDATE so the
// caller can read NextRowID and bump it atomically. Used by Insert /
// Upsert so concurrent writers never mint the same id.
func (s *PgService) lockMeta(tx *gorm.DB, slug string) (entity.DataTable, error) {
	var m entity.DataTable
	if err := tx.Set("gorm:query_option", "FOR UPDATE").
		Where("slug = ?", slug).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return m, fmt.Errorf("data table %q not registered", slug)
		}
		return m, err
	}
	return m, nil
}

// bumpNextRowID increments the per-slug counter and returns the
// freshly allocated id. Caller already holds the row lock via lockMeta.
func (s *PgService) bumpNextRowID(tx *gorm.DB, slug string, cur int64) (int64, error) {
	next := cur + 1
	if err := tx.Model(&entity.DataTable{}).Where("slug = ?", slug).
		Update("next_row_id", next).Error; err != nil {
		return 0, err
	}
	return next, nil
}

// stripDataKeys removes every JSONB key matching the dropped column
// ids from every row of the slug. Single bulk UPDATE per column —
// Postgres rewrites the JSONB column once per affected row.
func (s *PgService) stripDataKeys(tx *gorm.DB, slug string, dropped []Column) error {
	for _, c := range dropped {
		if c.ID == "" {
			continue
		}
		if err := tx.Exec(
			`UPDATE wick_data_table_rows SET "data" = "data" - ?, updated_at = now() WHERE table_slug = ? AND ("data" ? ?)`,
			c.ID, slug, c.ID,
		).Error; err != nil {
			return err
		}
		// Drop matching partial index if any.
		idxName := indexName(slug, c.ID)
		if q, err := quoteIdent(idxName); err == nil {
			_ = tx.Exec("DROP INDEX IF EXISTS " + q).Error
		}
	}
	return nil
}

// applyIndexes diffs prev↔next column lists and creates / drops the
// partial functional indexes for columns marked `indexed: true`.
// Index name embeds slug + column id so renames don't break it.
func (s *PgService) applyIndexes(tx *gorm.DB, slug string, next, prev []Column) error {
	prevByID := map[string]Column{}
	for _, c := range prev {
		if c.System {
			continue
		}
		prevByID[c.ID] = c
	}
	for _, c := range next {
		if c.System || c.ID == "" {
			continue
		}
		prevWanted := prevByID[c.ID].Indexed
		if c.Indexed && !prevWanted {
			if err := s.createIndex(tx, slug, c); err != nil {
				return err
			}
		}
		if !c.Indexed && prevWanted {
			if err := s.dropIndex(tx, slug, c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// dropAllIndexes drops every per-column partial index for a slug.
// Called during DropTable to leave Postgres clean.
func (s *PgService) dropAllIndexes(tx *gorm.DB, slug string, cols []Column) error {
	for _, c := range cols {
		if c.System || c.ID == "" {
			continue
		}
		if err := s.dropIndex(tx, slug, c.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgService) createIndex(tx *gorm.DB, slug string, c Column) error {
	idx := indexName(slug, c.ID)
	idxQ, err := quoteIdent(idx)
	if err != nil {
		return err
	}
	cast := pgIndexCast(c.Type)
	expr := fmt.Sprintf(`((data->>'%s')%s)`, c.ID, cast)
	// table_slug literal in the WHERE clause is fine — slug shape is
	// already validated by parse.ValidateID before any DDL runs.
	return tx.Exec(fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s ON wick_data_table_rows %s WHERE table_slug = '%s'`,
		idxQ, expr, slug,
	)).Error
}

func (s *PgService) dropIndex(tx *gorm.DB, slug, colID string) error {
	idx := indexName(slug, colID)
	idxQ, err := quoteIdent(idx)
	if err != nil {
		return nil // unsafe name should never make it here
	}
	return tx.Exec("DROP INDEX IF EXISTS " + idxQ).Error
}

// indexName returns the deterministic index name for a (slug, column
// id) pair. Hyphens in slug become underscores so the identifier is
// safe as a Postgres name.
func indexName(slug, colID string) string {
	return "idx_dtr_" + strings.ReplaceAll(slug, "-", "_") + "_" + colID
}
