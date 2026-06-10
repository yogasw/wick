// Package datatable is the workflow-facing shared-table store. The
// reference impl is the in-memory MockService below; production wires a
// Postgres backend that persists into `wick_data_tables` (schema) +
// `wick_data_table_rows` (data).
//
// Schema is owned by the Data Tables tool (`internal/tools/data-tables/`)
// and consumed here for row validation, indexed-column hints, and
// access enforcement. See `internal/planning/archive/workflow/12-data-tables.md`.
package datatable

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
)

// Schema is the per-data-table shape stored at
// `wick_data_tables.schema_json`.
//
// NextColID is the monotonic column-id allocator. Every Column.ID is
// `c<NextColID>` and NextColID only ever increments — even when a
// column is dropped, its id is "burned" and never reused. This keeps
// historical row data correctly orphaned (the old id maps to nothing,
// Decode ignores it) instead of accidentally being reinterpreted as a
// freshly-added column.
type Schema struct {
	Slug        string    `json:"-"`
	Name        string    `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Mode        string    `json:"mode,omitempty"` // strict | lax
	PrimaryKey  []string  `json:"primary_key,omitempty"`
	Columns     []Column  `json:"columns"`
	NextColID   int       `json:"next_col_id,omitempty"`
	Access      Access    `json:"access,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// Column is one column declaration.
//
// ID is the immutable storage key — values for this column live in
// JSONB under data[ID]. Renaming the column updates Name only; ID
// stays the same, so existing rows do not need to be rewritten. New
// IDs are minted by NextColumnID(&schema) from the per-table monotonic
// counter (Schema.NextColID).
//
// System columns (id, created_at, updated_at) are pseudo-columns: they
// do not live inside Data; the storage layer fills them from the
// composite primary key and timestamp columns. Their Column entries
// stay in Schema.Columns only so the UI can render the trio alongside
// user columns. ID is empty for system columns.
type Column struct {
	ID       string   `json:"id,omitempty"` // empty for system columns
	Name     string   `json:"name"`
	Type     string   `json:"type"` // string | int | float | bool | timestamp | json | enum
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Indexed  bool     `json:"indexed,omitempty"`
	Default  any      `json:"default,omitempty"`
	System   bool     `json:"system,omitempty"`
}

// Access limits which workflows can write the data table.
type Access struct {
	Workflows []string `json:"workflows,omitempty"`
	RowFilter string   `json:"row_filter,omitempty"` // by_creator | none
}

// Mode constants.
const (
	ModeStrict = "strict"
	ModeLax    = "lax"
)

// Reserved column names. Always present on every data table, always
// system-managed: `id` is the auto-incremented int PK, `created_at` and
// `updated_at` stamp themselves on row writes. Users cannot rename,
// drop, or hand-write these.
const (
	ColID        = "id"
	ColCreatedAt = "created_at"
	ColUpdatedAt = "updated_at"
)

// IsSystemColumn reports whether the name belongs to the reserved trio.
func IsSystemColumn(name string) bool {
	switch name {
	case ColID, ColCreatedAt, ColUpdatedAt:
		return true
	}
	return false
}

// systemColumns returns the reserved trio in canonical order. None of
// them are Required at the validation layer — the engine stamps them
// itself in Insert/Upsert after normalizeRow runs, so marking them
// required would reject every legitimate write.
func systemColumns() []Column {
	return []Column{
		{Name: ColID, Type: "int", System: true},
		{Name: ColCreatedAt, Type: "timestamp", System: true},
		{Name: ColUpdatedAt, Type: "timestamp", System: true},
	}
}

// ErrSchemaMismatch indicates the row violates the schema in strict mode.
var ErrSchemaMismatch = errors.New("data table row violates schema (strict mode)")

// Condition is one where-clause predicate. Mirrors the n8n Data Table
// node conditions (equals/not_equals/gt/gte/lt/lte/contains/in/is_empty/
// is_not_empty). Plain `where map[string]any` still works for the common
// equality case — Conditions is layered on top for the richer surface.
type Condition struct {
	Column string `json:"column"`
	Op     string `json:"op"` // equals | not_equals | gt | gte | lt | lte | contains | in | is_empty | is_not_empty
	Value  any    `json:"value,omitempty"`
}

// Supported condition op constants (n8n parity).
const (
	OpEquals      = "equals"
	OpNotEquals   = "not_equals"
	OpGT          = "gt"
	OpGTE         = "gte"
	OpLT          = "lt"
	OpLTE         = "lte"
	OpContains    = "contains"
	OpIn          = "in"
	OpIsEmpty     = "is_empty"
	OpIsNotEmpty  = "is_not_empty"
)

// Service is the workflow-facing data store contract.
type Service interface {
	// Table-level ops (n8n parity: create / list / update / delete table).
	CreateTable(schema Schema) error
	DropTable(slug string) error
	ListTables() []string

	LoadSchema(slug string) (Schema, error)
	SaveSchema(schema Schema) error

	// Column-level ops (spreadsheet UX parity).
	RenameColumn(slug, from, to string) error
	DropColumn(slug, name string) error

	// Row ops — equality where for backward compat.
	Insert(slug string, row map[string]any) error
	Upsert(slug string, row map[string]any) (action string, err error)
	Delete(slug string, where map[string]any) (int, error)
	Get(slug string, key map[string]any) (map[string]any, bool, error)
	Exists(slug string, where map[string]any) (bool, error)
	Count(slug string, where map[string]any) (int, error)
	Query(slug string, where map[string]any, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error)

	// Row ops — Condition list (n8n parity richer ops).
	QueryConditions(slug string, conditions []Condition, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error)
	DeleteConditions(slug string, conditions []Condition) (int, error)
	CountConditions(slug string, conditions []Condition) (int, error)
}

// MockService keeps data tables in memory.
type MockService struct {
	mu      sync.RWMutex
	schemas map[string]Schema
	rows    map[string][]map[string]any
	nextID  map[string]int64 // per-table auto-increment counter for `id`
}

// NewMock constructs an empty in-memory service. Test-only — production
// wires NewPg(db) via Manager.WithDataTablesDB so data survives restart.
func NewMock() *MockService {
	return &MockService{
		schemas: map[string]Schema{},
		rows:    map[string][]map[string]any{},
		nextID:  map[string]int64{},
	}
}

// CreateTable registers a brand-new table. The reserved trio
// (`id` int PK auto-increment, `created_at`, `updated_at`) is appended
// automatically; user columns slot in between. New user columns
// without a Column.ID are minted from the monotonic counter
// (Schema.NextColID) so storage rows are id-keyed.
//
// Errors if the slug already exists.
func (s *MockService) CreateTable(sc Schema) error {
	if err := parse.ValidateID(sc.Slug); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.schemas[sc.Slug]; ok {
		return fmt.Errorf("data table %q already exists", sc.Slug)
	}
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
	now := time.Now().UTC()
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = now
	}
	sc.UpdatedAt = now
	s.schemas[sc.Slug] = sc
	s.rows[sc.Slug] = nil
	s.nextID[sc.Slug] = 0
	return nil
}

// mergeSystemColumns strips any user-supplied entries for reserved names
// and re-inserts the canonical system trio. `id` goes first, the
// timestamps go last, user columns in the middle in their declared
// order. Idempotent.
func mergeSystemColumns(cols []Column) []Column {
	user := make([]Column, 0, len(cols))
	for _, c := range cols {
		if IsSystemColumn(c.Name) {
			continue
		}
		user = append(user, c)
	}
	out := make([]Column, 0, len(user)+3)
	out = append(out, Column{Name: ColID, Type: "int", System: true})
	out = append(out, user...)
	out = append(out, Column{Name: ColCreatedAt, Type: "timestamp", System: true})
	out = append(out, Column{Name: ColUpdatedAt, Type: "timestamp", System: true})
	return out
}

// DropTable removes a table and all its rows.
func (s *MockService) DropTable(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.schemas[slug]; !ok {
		return fmt.Errorf("data table %q not registered", slug)
	}
	delete(s.schemas, slug)
	delete(s.rows, slug)
	delete(s.nextID, slug)
	return nil
}

// ListTables returns every registered slug.
func (s *MockService) ListTables() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.schemas))
	for k := range s.schemas {
		out = append(out, k)
	}
	return out
}

// LoadSchema returns the registered schema.
func (s *MockService) LoadSchema(slug string) (Schema, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return Schema{}, fmt.Errorf("data table %q not registered", slug)
	}
	return sc, nil
}

// SaveSchema registers or replaces a schema (upsert semantics).
// Columns dropped relative to the previous schema also have their
// matching JSONB key stripped from every row, so storage and metadata
// stay consistent.
func (s *MockService) SaveSchema(sc Schema) error {
	if err := parse.ValidateID(sc.Slug); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var prevByID map[string]Column
	if prev, ok := s.schemas[sc.Slug]; ok {
		if sc.CreatedAt.IsZero() {
			sc.CreatedAt = prev.CreatedAt
		}
		if sc.NextColID < prev.NextColID {
			sc.NextColID = prev.NextColID
		}
		prevByID = map[string]Column{}
		for _, c := range prev.Columns {
			if c.System {
				continue
			}
			prevByID[c.ID] = c
		}
	} else if sc.CreatedAt.IsZero() {
		sc.CreatedAt = now
	}
	// Mint ids for new columns.
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
	sc.UpdatedAt = now
	// Strip data for columns removed in this save.
	for id := range prevByID {
		if _, kept := nextByID[id]; kept {
			continue
		}
		for _, row := range s.rows[sc.Slug] {
			delete(row, id)
		}
	}
	s.schemas[sc.Slug] = sc
	if _, ok := s.rows[sc.Slug]; !ok {
		s.rows[sc.Slug] = nil
		s.nextID[sc.Slug] = 0
	}
	return nil
}

// RenameColumn renames a user column. The storage id stays the same
// so no row is touched — only the Name field on the schema column
// changes.
func (s *MockService) RenameColumn(slug, from, to string) error {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return fmt.Errorf("data table %q not registered", slug)
	}
	srcIdx := -1
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
	sc.UpdatedAt = time.Now().UTC()
	s.schemas[slug] = sc
	return nil
}

// DropColumn removes one column from the schema and strips the
// matching id-keyed value from every stored row.
func (s *MockService) DropColumn(slug, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("drop column: name required")
	}
	if IsSystemColumn(name) {
		return fmt.Errorf("cannot drop system column %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return fmt.Errorf("data table %q not registered", slug)
	}
	var (
		idx    = -1
		target Column
	)
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
	sc.UpdatedAt = time.Now().UTC()
	s.schemas[slug] = sc
	if target.ID != "" {
		for _, row := range s.rows[slug] {
			delete(row, target.ID)
		}
	}
	return nil
}

// Insert appends a new row. `id` is auto-assigned (any user value is
// dropped); `created_at` + `updated_at` are stamped automatically.
// Row payload is encoded to column ids before storage so renames
// later don't touch data.
func (s *MockService) Insert(slug string, row map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return fmt.Errorf("data table %q not registered", slug)
	}
	stripSystemKeys(row)
	cleaned, err := normalizeRow(sc, row)
	if err != nil {
		return err
	}
	encoded, err := BuildIDMap(sc).Encode(cleaned, sc.Mode)
	if err != nil {
		return err
	}
	s.nextID[slug]++
	now := time.Now().UTC()
	encoded[ColID] = s.nextID[slug]
	encoded[ColCreatedAt] = now
	encoded[ColUpdatedAt] = now
	s.rows[slug] = append(s.rows[slug], encoded)
	return nil
}

// Upsert insert-or-updates by primary key. The caller may pass an `id`
// to target an existing row (legacy compat with MCP `datatable_upsert`).
// When the id is missing or unknown, behaves like Insert.
func (s *MockService) Upsert(slug string, row map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return "", fmt.Errorf("data table %q not registered", slug)
	}
	targetID, hasID := coerceInt64(row[ColID])
	delete(row, ColCreatedAt)
	delete(row, ColUpdatedAt)
	delete(row, ColID)
	cleaned, err := normalizeRow(sc, row)
	if err != nil {
		return "", err
	}
	encoded, err := BuildIDMap(sc).Encode(cleaned, sc.Mode)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if hasID {
		for i, existing := range s.rows[slug] {
			if eid, _ := coerceInt64(existing[ColID]); eid == targetID {
				createdAt := existing[ColCreatedAt]
				next := map[string]any{}
				for k, v := range encoded {
					next[k] = v
				}
				next[ColID] = targetID
				next[ColCreatedAt] = createdAt
				next[ColUpdatedAt] = now
				s.rows[slug][i] = next
				if targetID > s.nextID[slug] {
					s.nextID[slug] = targetID
				}
				return "update", nil
			}
		}
		s.nextID[slug] = max64(s.nextID[slug], targetID)
		encoded[ColID] = targetID
	} else {
		s.nextID[slug]++
		encoded[ColID] = s.nextID[slug]
	}
	encoded[ColCreatedAt] = now
	encoded[ColUpdatedAt] = now
	s.rows[slug] = append(s.rows[slug], encoded)
	return "insert", nil
}

// stripSystemKeys drops user attempts to set id/created_at/updated_at.
func stripSystemKeys(row map[string]any) {
	for _, k := range []string{ColID, ColCreatedAt, ColUpdatedAt} {
		delete(row, k)
	}
}

// coerceInt64 parses any numeric / numeric-string value as int64.
// Returns ok=false on nil / empty / unparseable input.
func coerceInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Delete removes all rows matching where (caller uses user-facing names).
func (s *MockService) Delete(slug string, where map[string]any) (int, error) {
	return s.deleteWithMap(slug, where, nil)
}

// DeleteConditions removes rows that match every Condition (AND semantics).
func (s *MockService) DeleteConditions(slug string, conds []Condition) (int, error) {
	return s.deleteWithMap(slug, nil, conds)
}

func (s *MockService) deleteWithMap(slug string, where map[string]any, conds []Condition) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return 0, fmt.Errorf("data table %q not registered", slug)
	}
	idmap := BuildIDMap(sc)
	encWhere, err := encodeWhere(idmap, where)
	if err != nil {
		return 0, err
	}
	encConds, err := encodeConditions(idmap, conds)
	if err != nil {
		return 0, err
	}
	rows := s.rows[slug]
	kept := rows[:0]
	deleted := 0
	for _, r := range rows {
		if rowMatches(r, encWhere, encConds) {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	s.rows[slug] = kept
	return deleted, nil
}

// Get returns the first row matching key (user-facing names) decoded
// back to user-facing names.
func (s *MockService) Get(slug string, key map[string]any) (map[string]any, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return nil, false, fmt.Errorf("data table %q not registered", slug)
	}
	idmap := BuildIDMap(sc)
	encKey, err := encodeWhere(idmap, key)
	if err != nil {
		return nil, false, err
	}
	for _, r := range s.rows[slug] {
		if rowMatches(r, encKey, nil) {
			return decodeRow(idmap, r), true, nil
		}
	}
	return nil, false, nil
}

// Exists reports whether any row matches where.
func (s *MockService) Exists(slug string, where map[string]any) (bool, error) {
	_, found, err := s.Get(slug, where)
	return found, err
}

// Count returns rows matching where.
func (s *MockService) Count(slug string, where map[string]any) (int, error) {
	return s.countWithMap(slug, where, nil)
}

// CountConditions counts rows that satisfy every condition.
func (s *MockService) CountConditions(slug string, conds []Condition) (int, error) {
	return s.countWithMap(slug, nil, conds)
}

func (s *MockService) countWithMap(slug string, where map[string]any, conds []Condition) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return 0, fmt.Errorf("data table %q not registered", slug)
	}
	idmap := BuildIDMap(sc)
	encWhere, err := encodeWhere(idmap, where)
	if err != nil {
		return 0, err
	}
	encConds, err := encodeConditions(idmap, conds)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range s.rows[slug] {
		if rowMatches(r, encWhere, encConds) {
			count++
		}
	}
	return count, nil
}

// Query returns matching rows with order + pagination (user-facing names).
func (s *MockService) Query(slug string, where map[string]any, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	return s.queryWithMap(slug, where, nil, order, limit, offset)
}

// QueryConditions returns matching rows for the richer condition list.
func (s *MockService) QueryConditions(slug string, conds []Condition, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	return s.queryWithMap(slug, nil, conds, order, limit, offset)
}

func (s *MockService) queryWithMap(slug string, where map[string]any, conds []Condition, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return nil, fmt.Errorf("data table %q not registered", slug)
	}
	idmap := BuildIDMap(sc)
	encWhere, err := encodeWhere(idmap, where)
	if err != nil {
		return nil, err
	}
	encConds, err := encodeConditions(idmap, conds)
	if err != nil {
		return nil, err
	}
	// Translate order columns name → id (system cols pass-through).
	encOrder := make([]workflow.DataTableOrder, 0, len(order))
	for _, o := range order {
		col := o.Column
		if !IsSystemColumn(col) {
			if cid, ok := idmap.NameToID[col]; ok {
				col = cid
			}
		}
		encOrder = append(encOrder, workflow.DataTableOrder{Column: col, Direction: o.Direction})
	}
	matched := []map[string]any{}
	for _, r := range s.rows[slug] {
		if rowMatches(r, encWhere, encConds) {
			matched = append(matched, r)
		}
	}
	matched = sortRows(matched, encOrder)
	if offset > 0 {
		if offset >= len(matched) {
			return []map[string]any{}, nil
		}
		matched = matched[offset:]
	}
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	out := make([]map[string]any, len(matched))
	for i, r := range matched {
		out[i] = decodeRow(idmap, r)
	}
	return out, nil
}

// encodeWhere translates user-facing keys → storage ids. System cols
// stay literal so caller can match on id/created_at/updated_at.
func encodeWhere(m IDMap, where map[string]any) (map[string]any, error) {
	if len(where) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	for k, v := range where {
		if IsSystemColumn(k) {
			out[k] = v
			continue
		}
		if cid, ok := m.NameToID[k]; ok {
			out[cid] = v
			continue
		}
		// Unknown name → no row can match. Use a sentinel that
		// matchWhere will never satisfy.
		return map[string]any{"__no_match__": struct{}{}}, nil
	}
	return out, nil
}

// encodeConditions translates Condition column names → ids.
func encodeConditions(m IDMap, conds []Condition) ([]Condition, error) {
	if len(conds) == 0 {
		return nil, nil
	}
	out := make([]Condition, 0, len(conds))
	for _, c := range conds {
		col := c.Column
		if !IsSystemColumn(col) {
			cid, ok := m.NameToID[col]
			if !ok {
				return nil, fmt.Errorf("unknown column %q", col)
			}
			col = cid
		}
		out = append(out, Condition{Column: col, Op: c.Op, Value: c.Value})
	}
	return out, nil
}

// decodeRow turns a stored (id-keyed) row into a caller-facing
// (name-keyed) row, copying system column values verbatim.
func decodeRow(m IDMap, stored map[string]any) map[string]any {
	out := m.Decode(stored)
	for _, k := range []string{ColID, ColCreatedAt, ColUpdatedAt} {
		if v, ok := stored[k]; ok {
			out[k] = v
		}
	}
	return out
}

// rowMatches combines equality where + condition list against a
// stored (id-keyed) row.
func rowMatches(r map[string]any, where map[string]any, conds []Condition) bool {
	if where != nil {
		if _, sentinel := where["__no_match__"]; sentinel {
			return false
		}
		if !matchWhere(r, where) {
			return false
		}
	}
	if !evalAll(r, conds) {
		return false
	}
	return true
}

func (s *MockService) queryFn(slug string, pred func(map[string]any) bool, order []workflow.DataTableOrder, limit, offset int) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.rows[slug]
	if !ok {
		return nil, fmt.Errorf("data table %q not registered", slug)
	}
	out := []map[string]any{}
	for _, r := range rows {
		if pred(r) {
			out = append(out, r)
		}
	}
	out = sortRows(out, order)
	if offset > 0 {
		if offset >= len(out) {
			return []map[string]any{}, nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// evalAll returns true when every condition holds on the row.
func evalAll(row map[string]any, conds []Condition) bool {
	for _, c := range conds {
		if !evalOne(row, c) {
			return false
		}
	}
	return true
}

func evalOne(row map[string]any, c Condition) bool {
	v, present := row[c.Column]
	switch c.Op {
	case OpIsEmpty:
		return !present || isEmpty(v)
	case OpIsNotEmpty:
		return present && !isEmpty(v)
	}
	if !present {
		return false
	}
	switch c.Op {
	case "", OpEquals:
		return fmt.Sprintf("%v", v) == fmt.Sprintf("%v", c.Value)
	case OpNotEquals:
		return fmt.Sprintf("%v", v) != fmt.Sprintf("%v", c.Value)
	case OpGT:
		return compareScalar(v, c.Value) > 0
	case OpGTE:
		return compareScalar(v, c.Value) >= 0
	case OpLT:
		return compareScalar(v, c.Value) < 0
	case OpLTE:
		return compareScalar(v, c.Value) <= 0
	case OpContains:
		return strings.Contains(strings.ToLower(fmt.Sprintf("%v", v)), strings.ToLower(fmt.Sprintf("%v", c.Value)))
	case OpIn:
		list, ok := c.Value.([]any)
		if !ok {
			// allow []string too
			if ss, ok2 := c.Value.([]string); ok2 {
				for _, s := range ss {
					if fmt.Sprintf("%v", v) == s {
						return true
					}
				}
				return false
			}
			return false
		}
		for _, item := range list {
			if fmt.Sprintf("%v", v) == fmt.Sprintf("%v", item) {
				return true
			}
		}
		return false
	}
	return false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// compareScalar returns -1/0/1 for numeric or lexicographic compare.
func compareScalar(a, b any) int {
	af, ai, ok := toNumber(a)
	bf, bi, ok2 := toNumber(b)
	if ok && ok2 {
		if ai && bi {
			ax := int64(af)
			bx := int64(bf)
			switch {
			case ax < bx:
				return -1
			case ax > bx:
				return 1
			}
			return 0
		}
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	}
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	switch {
	case as < bs:
		return -1
	case as > bs:
		return 1
	}
	return 0
}

func toNumber(v any) (f float64, isInt bool, ok bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true, true
	case int32:
		return float64(n), true, true
	case int64:
		return float64(n), true, true
	case float32:
		return float64(n), false, true
	case float64:
		return n, false, true
	}
	return 0, false, false
}

func normalizeRow(sc Schema, row map[string]any) (map[string]any, error) {
	cols := map[string]Column{}
	for _, c := range sc.Columns {
		cols[c.Name] = c
	}
	for _, c := range sc.Columns {
		v, present := row[c.Name]
		if !present {
			if c.Required {
				return nil, fmt.Errorf("%w: missing required column %q", ErrSchemaMismatch, c.Name)
			}
			if c.Default != nil {
				row[c.Name] = c.Default
			}
			continue
		}
		if !typeOK(c.Type, v) {
			return nil, fmt.Errorf("%w: column %q expected %s, got %T", ErrSchemaMismatch, c.Name, c.Type, v)
		}
		if c.Type == "enum" && len(c.Enum) > 0 {
			if s, ok := v.(string); ok && !containsStr(c.Enum, s) {
				return nil, fmt.Errorf("%w: column %q value %q not in enum %v", ErrSchemaMismatch, c.Name, s, c.Enum)
			}
		}
	}
	mode := sc.Mode
	if mode == "" {
		mode = ModeStrict
	}
	if mode == ModeStrict {
		for k := range row {
			if _, ok := cols[k]; !ok {
				return nil, fmt.Errorf("%w: extra column %q (strict mode)", ErrSchemaMismatch, k)
			}
		}
	}
	return row, nil
}

func typeOK(want string, v any) bool {
	if v == nil {
		return true
	}
	switch want {
	case "string", "enum":
		_, ok := v.(string)
		return ok
	case "int":
		switch v.(type) {
		case int, int32, int64, float64:
			return true
		}
		return false
	case "float":
		switch v.(type) {
		case float32, float64, int, int64:
			return true
		}
		return false
	case "bool":
		_, ok := v.(bool)
		return ok
	case "timestamp":
		switch v.(type) {
		case string, time.Time:
			return true
		}
		return false
	case "json":
		return true
	}
	return true
}

func rowsMatchPK(pk []string, a, b map[string]any) bool {
	for _, col := range pk {
		if fmt.Sprintf("%v", a[col]) != fmt.Sprintf("%v", b[col]) {
			return false
		}
	}
	return true
}

func matchWhere(row, where map[string]any) bool {
	for k, want := range where {
		got, ok := row[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			return false
		}
	}
	return true
}

func sortRows(rows []map[string]any, order []workflow.DataTableOrder) []map[string]any {
	if len(order) == 0 {
		return rows
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if compareRows(rows[i], rows[j], order) > 0 {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	return rows
}

func compareRows(a, b map[string]any, order []workflow.DataTableOrder) int {
	for _, o := range order {
		cmp := compareScalar(a[o.Column], b[o.Column])
		if cmp == 0 {
			continue
		}
		if o.Direction == "desc" {
			cmp = -cmp
		}
		return cmp
	}
	return 0
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
