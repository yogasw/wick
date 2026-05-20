package datatable_test

import (
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
)

func newServiceWithSchema(t *testing.T, mode string) *datatable.MockService {
	t.Helper()
	svc := datatable.NewMock()
	// CreateTable appends the reserved trio (id/created_at/updated_at)
	// automatically. PK is forced to id (auto-increment int).
	if err := svc.CreateTable(datatable.Schema{
		Slug: "events",
		Mode: mode,
		Columns: []datatable.Column{
			{Name: "status", Type: "enum", Enum: []string{"received", "processing", "done"}, Required: true},
			{Name: "priority", Type: "int"},
			{Name: "handled_at", Type: "timestamp"},
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	return svc
}

func TestCreateThenList(t *testing.T) {
	svc := datatable.NewMock()
	if err := svc.CreateTable(datatable.Schema{Slug: "a", Columns: []datatable.Column{{Name: "x", Type: "string"}}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateTable(datatable.Schema{Slug: "a"}); err == nil {
		t.Fatal("expected duplicate-slug error")
	}
	tables := svc.ListTables()
	if len(tables) != 1 || tables[0] != "a" {
		t.Fatalf("ListTables = %v", tables)
	}
}

func TestDropTable(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	if err := svc.Insert("events", map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DropTable("events"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LoadSchema("events"); err == nil {
		t.Fatal("LoadSchema after drop should fail")
	}
	if err := svc.DropTable("nope"); err == nil {
		t.Fatal("drop missing should fail")
	}
}

func TestInsertStrictRejectsExtraKey(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	err := svc.Insert("events", map[string]any{"status": "done", "extra": 42})
	if !errors.Is(err, datatable.ErrSchemaMismatch) {
		t.Fatalf("want ErrSchemaMismatch, got %v", err)
	}
}

func TestInsertLaxAcceptsExtraKey(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeLax)
	if err := svc.Insert("events", map[string]any{"status": "done", "extra": 42}); err != nil {
		t.Fatalf("lax insert should pass, got %v", err)
	}
}

func TestInsertRejectsTypeMismatch(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	err := svc.Insert("events", map[string]any{"status": "done", "priority": "high"})
	if !errors.Is(err, datatable.ErrSchemaMismatch) {
		t.Fatalf("want ErrSchemaMismatch, got %v", err)
	}
}

func TestInsertRejectsEnumOutOfList(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	err := svc.Insert("events", map[string]any{"status": "exploded"})
	if !errors.Is(err, datatable.ErrSchemaMismatch) {
		t.Fatalf("want ErrSchemaMismatch, got %v", err)
	}
}

func TestInsertAutoIncrementsID(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done"}))
	must(t, svc.Insert("events", map[string]any{"status": "done"}))
	rows, _ := svc.Query("events", nil, nil, 0, 0)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	ids := []int64{}
	for _, r := range rows {
		i, ok := r["id"].(int64)
		if !ok {
			t.Fatalf("id not int64: %T %v", r["id"], r["id"])
		}
		ids = append(ids, i)
	}
	if !((ids[0] == 1 && ids[1] == 2) || (ids[0] == 2 && ids[1] == 1)) {
		t.Fatalf("expected ids {1,2}, got %v", ids)
	}
}

func TestInsertStampsTimestamps(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done"}))
	rows, _ := svc.Query("events", nil, nil, 0, 0)
	if _, ok := rows[0]["created_at"]; !ok {
		t.Fatalf("created_at not set")
	}
	if _, ok := rows[0]["updated_at"]; !ok {
		t.Fatalf("updated_at not set")
	}
}

func TestInsertIgnoresUserSuppliedID(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"id": "should-be-stripped", "status": "done"}))
	rows, _ := svc.Query("events", nil, nil, 0, 0)
	if rows[0]["id"] != int64(1) {
		t.Fatalf("expected auto id=1, got %v", rows[0]["id"])
	}
}

func TestUpsertActions(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	act, err := svc.Upsert("events", map[string]any{"status": "received"})
	must(t, err)
	if act != "insert" {
		t.Fatalf("want insert, got %s", act)
	}
	act, err = svc.Upsert("events", map[string]any{"id": int64(1), "status": "done"})
	must(t, err)
	if act != "update" {
		t.Fatalf("want update, got %s", act)
	}
	row, found, _ := svc.Get("events", map[string]any{"id": int64(1)})
	if !found || row["status"] != "done" {
		t.Fatalf("post-upsert state wrong: %v", row)
	}
}

func TestExistsCountQuery(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 1}))
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 5}))
	must(t, svc.Insert("events", map[string]any{"status": "received", "priority": 10}))

	ok, _ := svc.Exists("events", map[string]any{"status": "done"})
	if !ok {
		t.Fatal("Exists status=done should be true")
	}
	c, _ := svc.Count("events", map[string]any{"status": "done"})
	if c != 2 {
		t.Fatalf("Count done = %d, want 2", c)
	}
	rows, err := svc.Query("events", map[string]any{"status": "done"}, []workflow.DataTableOrder{{Column: "priority", Direction: "desc"}}, 0, 0)
	must(t, err)
	if len(rows) != 2 || rows[0]["priority"] != 5 || rows[1]["priority"] != 1 {
		t.Fatalf("Query unsorted: %v", rows)
	}
}

func TestQueryLimitOffset(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	for i := 1; i <= 5; i++ {
		must(t, svc.Insert("events", map[string]any{"status": "done", "priority": i}))
	}
	rows, _ := svc.Query("events", map[string]any{"status": "done"}, []workflow.DataTableOrder{{Column: "priority"}}, 2, 1)
	if len(rows) != 2 {
		t.Fatalf("limit=2 returned %d rows", len(rows))
	}
	if rows[0]["priority"] != 2 || rows[1]["priority"] != 3 {
		t.Fatalf("offset=1 limit=2 mismatched: %v", rows)
	}
}

func TestQueryConditionsAllOps(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 1, "handled_at": "2026-01-01"}))
	must(t, svc.Insert("events", map[string]any{"status": "received", "priority": 5}))
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 10, "handled_at": "2026-05-01"}))

	tests := []struct {
		name   string
		conds  []datatable.Condition
		expect int
	}{
		{"equals", []datatable.Condition{{Column: "status", Op: datatable.OpEquals, Value: "done"}}, 2},
		{"not_equals", []datatable.Condition{{Column: "status", Op: datatable.OpNotEquals, Value: "done"}}, 1},
		{"gt", []datatable.Condition{{Column: "priority", Op: datatable.OpGT, Value: 1}}, 2},
		{"gte", []datatable.Condition{{Column: "priority", Op: datatable.OpGTE, Value: 5}}, 2},
		{"lt", []datatable.Condition{{Column: "priority", Op: datatable.OpLT, Value: 5}}, 1},
		{"lte", []datatable.Condition{{Column: "priority", Op: datatable.OpLTE, Value: 5}}, 2},
		{"contains", []datatable.Condition{{Column: "status", Op: datatable.OpContains, Value: "EIV"}}, 1},
		{"in", []datatable.Condition{{Column: "status", Op: datatable.OpIn, Value: []any{"done", "received"}}}, 3},
		{"is_empty", []datatable.Condition{{Column: "handled_at", Op: datatable.OpIsEmpty}}, 1},
		{"is_not_empty", []datatable.Condition{{Column: "handled_at", Op: datatable.OpIsNotEmpty}}, 2},
		{"compound_AND", []datatable.Condition{
			{Column: "status", Op: datatable.OpEquals, Value: "done"},
			{Column: "priority", Op: datatable.OpGTE, Value: 5},
		}, 1},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rows, err := svc.QueryConditions("events", tc.conds, nil, 0, 0)
			must(t, err)
			if len(rows) != tc.expect {
				t.Fatalf("rows=%d want=%d (rows: %v)", len(rows), tc.expect, rows)
			}
		})
	}
}

func TestDeleteWhereAndConditions(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 1}))
	must(t, svc.Insert("events", map[string]any{"status": "received"}))
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 9}))

	n, err := svc.Delete("events", map[string]any{"status": "received"})
	must(t, err)
	if n != 1 {
		t.Fatalf("Delete where status=received returned %d, want 1", n)
	}
	n, err = svc.DeleteConditions("events", []datatable.Condition{{Column: "priority", Op: datatable.OpGT, Value: 5}})
	must(t, err)
	if n != 1 {
		t.Fatalf("DeleteConditions priority>5 returned %d, want 1", n)
	}
	left, _ := svc.Count("events", nil)
	if left != 1 {
		t.Fatalf("post-delete count=%d, want 1", left)
	}
}

func TestSaveSchemaUpsertsExisting(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	// SaveSchema enforces the reserved trio + PK pin, so a minimal
	// caller schema results in exactly the trio (no user cols).
	must(t, svc.SaveSchema(datatable.Schema{Slug: "events"}))
	sc, _ := svc.LoadSchema("events")
	if len(sc.Columns) != 3 {
		t.Fatalf("schema not replaced (expected reserved trio): %+v", sc)
	}
}

func TestSaveSchemaRejectsBadSlug(t *testing.T) {
	svc := datatable.NewMock()
	err := svc.SaveSchema(datatable.Schema{Slug: "Bad Slug!"})
	if err == nil {
		t.Fatal("expected bad slug rejected")
	}
}

func TestRenameColumnRewritesRows(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 5}))
	must(t, svc.Insert("events", map[string]any{"status": "received", "priority": 9}))

	if err := svc.RenameColumn("events", "priority", "rank"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	sc, _ := svc.LoadSchema("events")
	for _, c := range sc.Columns {
		if c.Name == "priority" {
			t.Fatalf("priority column still present after rename")
		}
	}
	rows, _ := svc.Query("events", nil, nil, 0, 0)
	for _, r := range rows {
		if _, ok := r["priority"]; ok {
			t.Fatalf("row still has old key 'priority': %v", r)
		}
		if _, ok := r["rank"]; !ok {
			t.Fatalf("row missing new key 'rank': %v", r)
		}
	}
}

func TestRenameColumnRejectsSystem(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	if err := svc.RenameColumn("events", "id", "uuid"); err == nil {
		t.Fatal("expected system column rename rejection")
	}
	if err := svc.RenameColumn("events", "created_at", "ct"); err == nil {
		t.Fatal("expected created_at rename rejection")
	}
	if err := svc.RenameColumn("events", "status", "id"); err == nil {
		t.Fatal("expected rename-to-reserved rejection")
	}
}

func TestRenameColumnRejectsClash(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	if err := svc.RenameColumn("events", "priority", "status"); err == nil {
		t.Fatal("expected clash rejection")
	}
}

func TestDropColumnStripsRows(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	must(t, svc.Insert("events", map[string]any{"status": "done", "priority": 5}))
	must(t, svc.Insert("events", map[string]any{"status": "received"}))

	if err := svc.DropColumn("events", "priority"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	sc, _ := svc.LoadSchema("events")
	for _, c := range sc.Columns {
		if c.Name == "priority" {
			t.Fatalf("priority column still in schema after drop")
		}
	}
	rows, _ := svc.Query("events", nil, nil, 0, 0)
	for _, r := range rows {
		if _, ok := r["priority"]; ok {
			t.Fatalf("row still has priority key: %v", r)
		}
	}
}

func TestDropColumnRejectsSystem(t *testing.T) {
	svc := newServiceWithSchema(t, datatable.ModeStrict)
	for _, col := range []string{"id", "created_at", "updated_at"} {
		if err := svc.DropColumn("events", col); err == nil {
			t.Fatalf("expected drop of system column %q to be rejected", col)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pad(i int) string {
	switch {
	case i < 10:
		return "00" + itoa(i)
	case i < 100:
		return "0" + itoa(i)
	}
	return itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
