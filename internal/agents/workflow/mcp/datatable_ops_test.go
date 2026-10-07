package mcp

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow/datatable"
)

// DataTableCreate carries the creating user into Schema.UserID (persisted as
// created_by, the Owner column); an empty UserID leaves the table ownerless.
func TestDataTableCreate_UserID(t *testing.T) {
	m := &Ops{DataTables: datatable.NewMock()}
	cols := []datatable.Column{{Name: "status", Type: "string"}}

	if err := m.DataTableCreate(DataTableCreateInput{Slug: "owned", Columns: cols, UserID: "user-123"}); err != nil {
		t.Fatalf("create owned: %v", err)
	}
	if err := m.DataTableCreate(DataTableCreateInput{Slug: "system", Columns: cols}); err != nil {
		t.Fatalf("create system: %v", err)
	}
	for slug, want := range map[string]string{"owned": "user-123", "system": ""} {
		sc, err := m.DataTables.LoadSchema(slug)
		if err != nil {
			t.Fatalf("load %s: %v", slug, err)
		}
		if sc.UserID != want {
			t.Fatalf("%s: Schema.UserID = %q, want %q", slug, sc.UserID, want)
		}
	}
}
