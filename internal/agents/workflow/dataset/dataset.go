// Package dataset is the workflow-facing data store. The reference
// impl is the in-memory MemService below; production wires a Postgres
// backend that persists into `wick_datasets_rows`.
//
// Schema lives at `<BaseDir>/datasets/<slug>/dataset.yaml`. It drives
// row validation, indexed-column hints, and the version-pin guard.
package dataset

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
)

// Schema is the dataset.yaml shape.
type Schema struct {
	Slug        string    `yaml:"-"`
	Version     int       `yaml:"version"`
	Description string    `yaml:"description,omitempty"`
	Mode        string    `yaml:"mode,omitempty"` // strict | lax | extensible
	PrimaryKey  []string  `yaml:"primary_key,omitempty"`
	Columns     []Column  `yaml:"columns"`
	Access      Access    `yaml:"access,omitempty"`
	UpdatedAt   time.Time `yaml:"updated_at,omitempty"`
}

// Column is one column declaration.
type Column struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"` // string | int | float | bool | timestamp | json | enum
	Required bool     `yaml:"required,omitempty"`
	Enum     []string `yaml:"enum,omitempty"`
	Indexed  bool     `yaml:"indexed,omitempty"`
	Default  any      `yaml:"default,omitempty"`
}

// Access limits which workflows can write the dataset.
type Access struct {
	Workflows []string `yaml:"workflows,omitempty"`
	RowFilter string   `yaml:"row_filter,omitempty"` // by_creator | none
}

// Mode constants.
const (
	ModeStrict     = "strict"
	ModeLax        = "lax"
	ModeExtensible = "extensible"
)

// ErrSchemaMismatch indicates the row violates the schema in strict mode.
var ErrSchemaMismatch = errors.New("dataset row violates schema (strict mode)")

// ErrVersionMismatch is returned when a workflow's expected_version
// no longer matches the dataset's current version.
var ErrVersionMismatch = errors.New("dataset version mismatch")

// Service is the workflow-facing data store contract.
type Service interface {
	LoadSchema(slug string) (Schema, error)
	SaveSchema(schema Schema) error
	Insert(slug string, row map[string]any) error
	Upsert(slug string, row map[string]any) (action string, err error) // "insert" | "update"
	Delete(slug string, where map[string]any) (int, error)
	Get(slug string, key map[string]any) (map[string]any, bool, error)
	Exists(slug string, where map[string]any) (bool, error)
	Count(slug string, where map[string]any) (int, error)
	Query(slug string, where map[string]any, order []workflow.DatasetOrder, limit, offset int) ([]map[string]any, error)
}

// MemService keeps datasets in memory.
type MemService struct {
	mu      sync.RWMutex
	schemas map[string]Schema
	rows    map[string][]map[string]any
}

// NewMem constructs an empty in-memory service.
func NewMem() *MemService {
	return &MemService{
		schemas: map[string]Schema{},
		rows:    map[string][]map[string]any{},
	}
}

// LoadSchema returns the registered schema.
func (s *MemService) LoadSchema(slug string) (Schema, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return Schema{}, fmt.Errorf("dataset %q not registered", slug)
	}
	return sc, nil
}

// SaveSchema registers or replaces a schema.
func (s *MemService) SaveSchema(sc Schema) error {
	if err := parse.ValidateID(sc.Slug); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemas[sc.Slug] = sc
	if _, ok := s.rows[sc.Slug]; !ok {
		s.rows[sc.Slug] = nil
	}
	return nil
}

// Insert appends a new row.
func (s *MemService) Insert(slug string, row map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return fmt.Errorf("dataset %q not registered", slug)
	}
	row, err := normalizeRow(sc, row)
	if err != nil {
		return err
	}
	if len(sc.PrimaryKey) > 0 {
		for _, existing := range s.rows[slug] {
			if rowsMatchPK(sc.PrimaryKey, existing, row) {
				return fmt.Errorf("dataset %q insert: PK %v already present", slug, sc.PrimaryKey)
			}
		}
	}
	s.rows[slug] = append(s.rows[slug], row)
	return nil
}

// Upsert insert-or-updates by PK.
func (s *MemService) Upsert(slug string, row map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schemas[slug]
	if !ok {
		return "", fmt.Errorf("dataset %q not registered", slug)
	}
	row, err := normalizeRow(sc, row)
	if err != nil {
		return "", err
	}
	if len(sc.PrimaryKey) > 0 {
		for i, existing := range s.rows[slug] {
			if rowsMatchPK(sc.PrimaryKey, existing, row) {
				s.rows[slug][i] = row
				return "update", nil
			}
		}
	}
	s.rows[slug] = append(s.rows[slug], row)
	return "insert", nil
}

// Delete removes all rows matching where; returns count.
func (s *MemService) Delete(slug string, where map[string]any) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, ok := s.rows[slug]
	if !ok {
		return 0, fmt.Errorf("dataset %q not registered", slug)
	}
	kept := rows[:0]
	deleted := 0
	for _, r := range rows {
		if matchWhere(r, where) {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	s.rows[slug] = kept
	return deleted, nil
}

// Get returns the first row matching key.
func (s *MemService) Get(slug string, key map[string]any) (map[string]any, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.rows[slug]
	if !ok {
		return nil, false, fmt.Errorf("dataset %q not registered", slug)
	}
	for _, r := range rows {
		if matchWhere(r, key) {
			return r, true, nil
		}
	}
	return nil, false, nil
}

// Exists reports whether any row matches where.
func (s *MemService) Exists(slug string, where map[string]any) (bool, error) {
	_, found, err := s.Get(slug, where)
	return found, err
}

// Count returns the number of rows matching where.
func (s *MemService) Count(slug string, where map[string]any) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.rows[slug]
	if !ok {
		return 0, fmt.Errorf("dataset %q not registered", slug)
	}
	count := 0
	for _, r := range rows {
		if matchWhere(r, where) {
			count++
		}
	}
	return count, nil
}

// Query returns matching rows with order + pagination.
func (s *MemService) Query(slug string, where map[string]any, order []workflow.DatasetOrder, limit, offset int) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.rows[slug]
	if !ok {
		return nil, fmt.Errorf("dataset %q not registered", slug)
	}
	out := []map[string]any{}
	for _, r := range rows {
		if matchWhere(r, where) {
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

func sortRows(rows []map[string]any, order []workflow.DatasetOrder) []map[string]any {
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

func compareRows(a, b map[string]any, order []workflow.DatasetOrder) int {
	for _, o := range order {
		av := fmt.Sprintf("%v", a[o.Column])
		bv := fmt.Sprintf("%v", b[o.Column])
		cmp := 0
		switch {
		case av < bv:
			cmp = -1
		case av > bv:
			cmp = 1
		}
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

// ParseSchema decodes a dataset.yaml body.
func ParseSchema(slug string, data []byte) (Schema, error) {
	var sc Schema
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return Schema{}, err
	}
	sc.Slug = slug
	if sc.Mode == "" {
		sc.Mode = ModeStrict
	}
	return sc, nil
}

// MarshalSchema serializes a schema to YAML.
func MarshalSchema(sc Schema) ([]byte, error) {
	sc.Slug = ""
	return yaml.Marshal(sc)
}
