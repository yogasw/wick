package datatable

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/entity"
)

// PgService is wired for EVERY dialect (setup.Manager.WithDataTablesDB
// calls NewPg unconditionally) while wick supports both postgres and
// sqlite. These tests pin down which operations actually survive on
// sqlite, so a Postgres-only construct cannot reach an sqlite install
// unnoticed again.
func sqliteService(t *testing.T) *PgService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.DataTable{}, &entity.DataTableRow{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewPg(db)
}

func listingsSchema() Schema {
	return Schema{
		Slug: "listings",
		Mode: "strict",
		Columns: []Column{
			{ID: "c1", Name: "harga_juta", Type: "string"},
			{ID: "c2", Name: "last_seen", Type: "string"},
		},
	}
}

// Insert is plain GORM with bound values, so it was expected to work —
// this is the control that proves the harness itself is sound.
func TestSQLiteInsertWorks(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// Reproduces the reported failure: Upsert merges the payload with
// `COALESCE("data",'{}'::jsonb) || ?::jsonb`. sqlite has no `::` cast —
// its tokenizer reads `:` as a named-parameter prefix and rejects the
// bare colon, giving `unrecognized token: ":"`.
func TestSQLiteUpsertUpdatesExistingRow(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := s.Upsert("listings", map[string]any{"harga_juta": "250"}); err != nil {
		t.Fatalf("first upsert (insert path): %v", err)
	}

	action, err := s.Upsert("listings", map[string]any{"id": 1, "harga_juta": "195"})
	if err != nil {
		t.Fatalf("upsert onto an existing id failed: %v", err)
	}
	if action != "update" {
		t.Fatalf("action = %q, want update", action)
	}

	row, ok, err := s.Get("listings", map[string]any{"id": 1})
	if err != nil || !ok {
		t.Fatalf("get: %v (found=%v)", err, ok)
	}
	if row["harga_juta"] != "195" {
		t.Fatalf("harga_juta = %v, want 195", row["harga_juta"])
	}
}

// The merge must PATCH, not replace: keys absent from the payload have
// to survive. That is what the postgres `||` operator bought, and any
// dialect-neutral replacement has to keep it.
func TestSQLiteUpsertPreservesUntouchedKeys(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := s.Upsert("listings", map[string]any{"harga_juta": "250", "last_seen": "2026-08-01"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.Upsert("listings", map[string]any{"id": 1, "harga_juta": "195"}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	row, ok, err := s.Get("listings", map[string]any{"id": 1})
	if err != nil || !ok {
		t.Fatalf("get: %v (found=%v)", err, ok)
	}
	if row["last_seen"] != "2026-08-01" {
		t.Fatalf("last_seen = %v; a key absent from the payload was dropped", row["last_seen"])
	}
	if row["harga_juta"] != "195" {
		t.Fatalf("harga_juta = %v, want 195", row["harga_juta"])
	}
}

// Query goes through data->>'cid', which modern sqlite understands.
// Included so the blast radius of the postgres-only SQL is measured
// rather than assumed.
func TestSQLiteQueryByEquality(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := s.Query("listings", map[string]any{"harga_juta": "250"}, nil, 0, 0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

// stripDataKeys drops the key from every row. It used three
// postgres-only constructs: `-` to delete a jsonb key, `?` to test key
// existence — which collides with sqlite's own positional placeholder —
// and now(), which sqlite does not have.
func TestSQLiteDropColumn(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250", "last_seen": "x"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.DropColumn("listings", "last_seen"); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	row, ok, err := s.Get("listings", map[string]any{"id": 1})
	if err != nil || !ok {
		t.Fatalf("get: %v (found=%v)", err, ok)
	}
	if _, still := row["last_seen"]; still {
		t.Fatal("the dropped column's key survived in the row data")
	}
	if row["harga_juta"] != "250" {
		t.Fatalf("harga_juta = %v; dropping one key disturbed another", row["harga_juta"])
	}
}

// typedSchema exercises the casting path: every non-string column type
// renders a cast in predicates, ORDER BY, and the partial index.
func typedSchema() Schema {
	return Schema{
		Slug: "typed",
		Mode: "strict",
		Columns: []Column{
			{ID: "c1", Name: "qty", Type: "int", Indexed: true},
			{ID: "c2", Name: "ratio", Type: "float"},
			{ID: "c3", Name: "active", Type: "bool"},
			{ID: "c4", Name: "note", Type: "string"},
		},
	}
}

// CreateTable builds a partial expression index per indexed column, with
// the column type cast baked into the expression.
func TestSQLiteCreateTableWithIndexedTypedColumn(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(typedSchema()); err != nil {
		t.Fatalf("create table with an indexed int column: %v", err)
	}
}

// Numeric comparison has to compare as a number, not as text: as text
// "9" > "10". That is what the cast buys, so the test asserts ordering
// semantics rather than merely that the SQL parsed.
func TestSQLiteNumericComparisonUsesNumericOrder(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(typedSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, q := range []any{9, 10, 200} {
		if err := s.Insert("typed", map[string]any{"qty": q, "note": "x"}); err != nil {
			t.Fatalf("insert %v: %v", q, err)
		}
	}
	got, err := s.CountConditions("typed", []Condition{{Column: "qty", Op: OpGT, Value: 10}})
	if err != nil {
		t.Fatalf("count gt: %v", err)
	}
	if got != 1 {
		t.Fatalf("qty > 10 matched %d rows, want 1 (text ordering would match 9 too)", got)
	}
}

func TestSQLiteOrderByTypedColumn(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(typedSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, q := range []any{200, 9, 10} {
		if err := s.Insert("typed", map[string]any{"qty": q}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	rows, err := s.Query("typed", nil, []workflow.DataTableOrder{{Column: "qty", Direction: "asc"}}, 0, 0)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows", len(rows))
	}
	if fmt.Sprintf("%v", rows[0]["qty"]) != "9" {
		t.Fatalf("first row qty = %v, want 9 (numeric order)", rows[0]["qty"])
	}
}

// `contains` is spelled ILIKE, which postgres has and sqlite does not.
// sqlite's LIKE is already case-insensitive for ASCII, so the behaviour
// must match, not merely parse.
func TestSQLiteContainsIsCaseInsensitive(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "Rumah Palembang"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.CountConditions("listings", []Condition{
		{Column: "harga_juta", Op: OpContains, Value: "palembang"},
	})
	if err != nil {
		t.Fatalf("contains: %v", err)
	}
	if got != 1 {
		t.Fatalf("case-insensitive contains matched %d rows, want 1", got)
	}
}

// is_empty / is_not_empty use the jsonb key-existence operator `?`,
// which sqlite parses as a positional placeholder.
func TestSQLiteIsEmptyAndIsNotEmpty(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250", "last_seen": "2026-08-01"}); err != nil {
		t.Fatalf("insert filled: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "195"}); err != nil {
		t.Fatalf("insert sparse: %v", err)
	}

	empty, err := s.CountConditions("listings", []Condition{{Column: "last_seen", Op: OpIsEmpty}})
	if err != nil {
		t.Fatalf("is_empty: %v", err)
	}
	if empty != 1 {
		t.Fatalf("is_empty matched %d, want 1", empty)
	}

	filled, err := s.CountConditions("listings", []Condition{{Column: "last_seen", Op: OpIsNotEmpty}})
	if err != nil {
		t.Fatalf("is_not_empty: %v", err)
	}
	if filled != 1 {
		t.Fatalf("is_not_empty matched %d, want 1", filled)
	}
}

// Renaming a column touches only the schema row, so this is the cheap
// half; it is here to prove the schema-edit path as a whole survives.
func TestSQLiteRenameColumn(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RenameColumn("listings", "last_seen", "seen_at"); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

// SaveSchema with a column removed goes through the same stripDataKeys
// bulk update DropColumn uses, so it is the second door onto that bug.
func TestSQLiteSaveSchemaDroppingAColumn(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250", "last_seen": "x"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	sc, err := s.LoadSchema("listings")
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	sc.Columns = sc.Columns[:1] // drop last_seen
	if err := s.SaveSchema(sc); err != nil {
		t.Fatalf("save schema with a dropped column: %v", err)
	}
}

// Update is not Upsert with a flag. Upsert INSERTS at whatever id it is
// handed, so a mistyped id produces a new row that looks like a
// successful edit — the failure mode a price tracker would notice only
// as duplicated data. Update has to report the miss instead.
func TestSQLiteUpdateDoesNotCreateRows(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250"}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	found, err := s.Update("listings", map[string]any{"id": 999, "harga_juta": "1"})
	if err != nil {
		t.Fatalf("update on a missing id errored instead of reporting the miss: %v", err)
	}
	if found {
		t.Fatal("update reported a hit on an id that does not exist")
	}
	n, err := s.Count("listings", nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("row count = %d, want 1 — update must never create a row", n)
	}
}

// The contrast that justifies having both ops: the same call through
// Upsert DOES create the row.
func TestSQLiteUpsertCreatesAtAGuessedID(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Upsert("listings", map[string]any{"id": 999, "harga_juta": "1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	n, _ := s.Count("listings", nil)
	if n != 1 {
		t.Fatalf("row count = %d, want 1 — upsert inserts at the requested id", n)
	}
}

// Update patches: keys absent from the payload keep their values.
func TestSQLiteUpdatePatchesAndPreservesKeys(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Insert("listings", map[string]any{"harga_juta": "250", "last_seen": "2026-08-01"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	found, err := s.Update("listings", map[string]any{"id": 1, "harga_juta": "195"})
	if err != nil || !found {
		t.Fatalf("update: %v (found=%v)", err, found)
	}
	row, ok, err := s.Get("listings", map[string]any{"id": 1})
	if err != nil || !ok {
		t.Fatalf("get: %v (found=%v)", err, ok)
	}
	if row["harga_juta"] != "195" {
		t.Fatalf("harga_juta = %v, want 195", row["harga_juta"])
	}
	if row["last_seen"] != "2026-08-01" {
		t.Fatalf("last_seen = %v; an untouched key was dropped", row["last_seen"])
	}
}

// Without an id there is nothing to match, and defaulting to "create"
// would make Update behave like Upsert exactly when the caller was least
// specific.
func TestSQLiteUpdateRequiresAnID(t *testing.T) {
	s := sqliteService(t)
	if err := s.CreateTable(listingsSchema()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Update("listings", map[string]any{"harga_juta": "195"}); err == nil {
		t.Fatal("update without an id must be refused, not treated as a create")
	}
}
