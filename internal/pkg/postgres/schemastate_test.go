package postgres

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Fingerprint probes. All share one table name so two shapes of the "same"
// table can be compared: a fingerprint must change exactly when AutoMigrate
// would emit different DDL for it.

type fpProbe struct {
	ID   uint
	Name string
}

func (fpProbe) TableName() string { return "fp_probe" }

type fpProbeExtraColumn struct {
	ID    uint
	Name  string
	Email string
}

func (fpProbeExtraColumn) TableName() string { return "fp_probe" }

type fpProbeWiderColumn struct {
	ID   uint
	Name string `gorm:"size:1024"`
}

func (fpProbeWiderColumn) TableName() string { return "fp_probe" }

type fpProbeIndexedColumn struct {
	ID   uint
	Name string `gorm:"index"`
}

func (fpProbeIndexedColumn) TableName() string { return "fp_probe" }

type fpProbeReordered struct {
	Name string
	ID   uint
}

func (fpProbeReordered) TableName() string { return "fp_probe" }

func fingerprintDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %s", err)
	}
	return db
}

func TestModelFingerprintIsDeterministic(t *testing.T) {
	db := fingerprintDB(t)
	a := modelFingerprint(db, []any{&fpProbe{}})
	b := modelFingerprint(db, []any{&fpProbe{}})
	if a == "" {
		t.Fatal("fingerprint of a valid model must not be empty")
	}
	if a != b {
		t.Fatalf("same model produced two fingerprints: %q vs %q", a, b)
	}
}

func TestModelFingerprintChangesWhenAColumnIsAdded(t *testing.T) {
	db := fingerprintDB(t)
	if modelFingerprint(db, []any{&fpProbe{}}) == modelFingerprint(db, []any{&fpProbeExtraColumn{}}) {
		t.Fatal("adding a column must change the fingerprint, or the new column is never created")
	}
}

func TestModelFingerprintChangesWhenAColumnWidens(t *testing.T) {
	db := fingerprintDB(t)
	if modelFingerprint(db, []any{&fpProbe{}}) == modelFingerprint(db, []any{&fpProbeWiderColumn{}}) {
		t.Fatal("changing a column size must change the fingerprint, or the ALTER never runs")
	}
}

// The proposal this file implements hashed only column name/type/null/unique.
// A `gorm:"index"` tag alters none of those, so the index would silently
// never be created. Indexes must be part of the hash.
func TestModelFingerprintChangesWhenAnIndexIsAdded(t *testing.T) {
	db := fingerprintDB(t)
	if modelFingerprint(db, []any{&fpProbe{}}) == modelFingerprint(db, []any{&fpProbeIndexedColumn{}}) {
		t.Fatal("adding an index tag must change the fingerprint, or the index is never created")
	}
}

func TestModelFingerprintIgnoresStructFieldOrder(t *testing.T) {
	db := fingerprintDB(t)
	if modelFingerprint(db, []any{&fpProbe{}}) != modelFingerprint(db, []any{&fpProbeReordered{}}) {
		t.Fatal("reordering struct fields emits no DDL, so it must not change the fingerprint")
	}
}

// An unparseable model means the fingerprint cannot vouch for the schema.
// "" tells the caller "unknown" — Migrate treats that as "run AutoMigrate",
// so a parse failure degrades to the old always-migrate behavior, never to
// a silently stale schema.
func TestModelFingerprintFailsClosedOnUnparseableModel(t *testing.T) {
	db := fingerprintDB(t)
	if fp := modelFingerprint(db, []any{&fpProbe{}, 42}); fp != "" {
		t.Fatalf("unparseable model must yield an empty fingerprint, got %q", fp)
	}
}

func TestStoredFingerprintRoundTrips(t *testing.T) {
	db := fingerprintDB(t)
	if got := storedFingerprint(db); got != "" {
		t.Fatalf("fresh database must have no stored fingerprint, got %q", got)
	}
	storeFingerprint(db, "abc123")
	if got := storedFingerprint(db); got != "abc123" {
		t.Fatalf("stored %q, read back %q", "abc123", got)
	}
	// Overwrite, not append: the state table holds exactly one row.
	storeFingerprint(db, "def456")
	if got := storedFingerprint(db); got != "def456" {
		t.Fatalf("overwrote with %q, read back %q", "def456", got)
	}
}

func TestMigrateStoresTheFingerprintItApplied(t *testing.T) {
	resetMigrated(t)
	db := fingerprintDB(t)
	Migrate(db)
	want := modelFingerprint(db, migratedModels)
	if want == "" {
		t.Fatal("production model list must be fingerprintable")
	}
	if got := storedFingerprint(db); got != want {
		t.Fatalf("after Migrate the stored fingerprint must match the models: got %q want %q", got, want)
	}
}

// The point of the whole feature: a database whose stored fingerprint
// already matches the model definitions gets no AutoMigrate pass.
// Observable here because the tables genuinely do not exist afterwards —
// only the pre-seeded state row does.
func TestMigrateSkipsAutoMigrateWhenTheFingerprintMatches(t *testing.T) {
	resetMigrated(t)
	db := fingerprintDB(t)
	fp := modelFingerprint(db, migratedModels)
	if fp == "" {
		t.Fatal("production model list must be fingerprintable")
	}
	storeFingerprint(db, fp)

	Migrate(db)

	if db.Migrator().HasTable("users") {
		t.Fatal("matching fingerprint must skip AutoMigrate, but tables were created")
	}
}

func TestMigrateRunsWhenTheStoredFingerprintIsStale(t *testing.T) {
	resetMigrated(t)
	db := fingerprintDB(t)
	storeFingerprint(db, "fingerprint-of-an-older-release")

	Migrate(db)

	if !db.Migrator().HasTable("users") {
		t.Fatal("stale fingerprint must trigger a full AutoMigrate pass")
	}
	if got := storedFingerprint(db); got != modelFingerprint(db, migratedModels) {
		t.Fatal("a completed pass must record the new fingerprint")
	}
}
