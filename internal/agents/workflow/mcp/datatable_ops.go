// Package mcp — Data Tables MCP surface. Mirrors the n8n Data Table
// node + tab: schema CRUD + row CRUD + condition-based filtering.
package mcp

import (
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
)

// DataTableSummary is the row shape for datatable_list.
type DataTableSummary struct {
	Slug     string `json:"slug"`
	Name     string `json:"name,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Columns  int    `json:"columns"`
	RowCount int    `json:"row_count"`
}

// DataTableList returns every registered table with row count.
func (m *Ops) DataTableList() ([]DataTableSummary, error) {
	if m.DataTables == nil {
		return nil, fmt.Errorf("data tables not configured")
	}
	slugs := m.DataTables.ListTables()
	out := make([]DataTableSummary, 0, len(slugs))
	for _, slug := range slugs {
		sc, err := m.DataTables.LoadSchema(slug)
		if err != nil {
			continue
		}
		count, _ := m.DataTables.Count(slug, nil)
		out = append(out, DataTableSummary{
			Slug:     slug,
			Mode:     sc.Mode,
			Columns:  len(sc.Columns),
			RowCount: count,
		})
	}
	return out, nil
}

// DataTableGet returns schema + sample rows for one table.
func (m *Ops) DataTableGet(slug string) (map[string]any, error) {
	if m.DataTables == nil {
		return nil, fmt.Errorf("data tables not configured")
	}
	sc, err := m.DataTables.LoadSchema(slug)
	if err != nil {
		return nil, err
	}
	count, _ := m.DataTables.Count(slug, nil)
	return map[string]any{
		"slug":      slug,
		"schema":    sc,
		"row_count": count,
	}, nil
}

// DataTableCreateInput is the payload for datatable_create.
type DataTableCreateInput struct {
	Slug       string              `json:"slug"`
	Mode       string              `json:"mode,omitempty"`
	PrimaryKey []string            `json:"primary_key,omitempty"`
	Columns    []datatable.Column  `json:"columns"`
	Access     *datatable.Access   `json:"access,omitempty"`
}

// DataTableCreate registers a new table.
func (m *Ops) DataTableCreate(in DataTableCreateInput) error {
	if m.DataTables == nil {
		return fmt.Errorf("data tables not configured")
	}
	sc := datatable.Schema{
		Slug:       in.Slug,
		Mode:       in.Mode,
		PrimaryKey: in.PrimaryKey,
		Columns:    in.Columns,
	}
	if in.Access != nil {
		sc.Access = *in.Access
	}
	if len(sc.PrimaryKey) == 0 && len(sc.Columns) > 0 {
		sc.PrimaryKey = []string{sc.Columns[0].Name}
	}
	return m.DataTables.CreateTable(sc)
}

// DataTableUpdateSchema replaces the schema of an existing table.
func (m *Ops) DataTableUpdateSchema(slug string, sc datatable.Schema) error {
	if m.DataTables == nil {
		return fmt.Errorf("data tables not configured")
	}
	sc.Slug = slug
	return m.DataTables.SaveSchema(sc)
}

// DataTableDrop removes a table and all its rows.
func (m *Ops) DataTableDrop(slug string) error {
	if m.DataTables == nil {
		return fmt.Errorf("data tables not configured")
	}
	return m.DataTables.DropTable(slug)
}

// DataTableQueryInput is the payload for datatable_query.
type DataTableQueryInput struct {
	Slug       string                       `json:"slug"`
	Where      map[string]any               `json:"where,omitempty"`
	Conditions []datatable.Condition        `json:"conditions,omitempty"`
	OrderBy    []workflow.DataTableOrder    `json:"order_by,omitempty"`
	Limit      int                          `json:"limit,omitempty"`
	Offset     int                          `json:"offset,omitempty"`
}

// DataTableQuery returns rows matching either Where (equality) or
// Conditions (richer ops). When both are set, Conditions wins.
func (m *Ops) DataTableQuery(in DataTableQueryInput) ([]map[string]any, error) {
	if m.DataTables == nil {
		return nil, fmt.Errorf("data tables not configured")
	}
	if len(in.Conditions) > 0 {
		return m.DataTables.QueryConditions(in.Slug, in.Conditions, in.OrderBy, in.Limit, in.Offset)
	}
	return m.DataTables.Query(in.Slug, in.Where, in.OrderBy, in.Limit, in.Offset)
}

// DataTableInsertInput is the payload for datatable_insert / upsert.
type DataTableInsertInput struct {
	Slug string         `json:"slug"`
	Row  map[string]any `json:"row"`
}

// DataTableInsert inserts a new row.
func (m *Ops) DataTableInsert(in DataTableInsertInput) error {
	if m.DataTables == nil {
		return fmt.Errorf("data tables not configured")
	}
	return m.DataTables.Insert(in.Slug, in.Row)
}

// DataTableUpsert insert-or-updates by PK.
func (m *Ops) DataTableUpsert(in DataTableInsertInput) (string, error) {
	if m.DataTables == nil {
		return "", fmt.Errorf("data tables not configured")
	}
	return m.DataTables.Upsert(in.Slug, in.Row)
}

// DataTableDeleteInput is the payload for datatable_delete.
type DataTableDeleteInput struct {
	Slug       string                `json:"slug"`
	Where      map[string]any        `json:"where,omitempty"`
	Conditions []datatable.Condition `json:"conditions,omitempty"`
}

// DataTableDelete removes rows; Conditions wins over Where when both set.
func (m *Ops) DataTableDelete(in DataTableDeleteInput) (int, error) {
	if m.DataTables == nil {
		return 0, fmt.Errorf("data tables not configured")
	}
	if len(in.Conditions) > 0 {
		return m.DataTables.DeleteConditions(in.Slug, in.Conditions)
	}
	return m.DataTables.Delete(in.Slug, in.Where)
}

// DataTableCount counts rows.
func (m *Ops) DataTableCount(in DataTableDeleteInput) (int, error) {
	if m.DataTables == nil {
		return 0, fmt.Errorf("data tables not configured")
	}
	if len(in.Conditions) > 0 {
		return m.DataTables.CountConditions(in.Slug, in.Conditions)
	}
	return m.DataTables.Count(in.Slug, in.Where)
}
