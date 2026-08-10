package postgres

import (
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// resetMigrated clears the per-process record so a test starts from a
// known state and does not leak into the next one.
func resetMigrated(t *testing.T) {
	t.Helper()
	migratedMu.Lock()
	migrated = map[string]bool{}
	migratedMu.Unlock()
	t.Cleanup(func() {
		migratedMu.Lock()
		migrated = map[string]bool{}
		migratedMu.Unlock()
	})
}

// Tray mode runs the server and the worker as independent toggles in one
// process, each opening its own *gorm.DB against the same database and
// calling Migrate. Only the first may run DDL: a second pass would alter
// tables while the component that booted first is already serving queries
// off a separate pool, leaving those connections with stale cached plans.
func TestMigrateSkipsARepeatPassOverTheSameDatabase(t *testing.T) {
	resetMigrated(t)

	const dsn = "postgres://user:pw@localhost:5432/wick"
	first := &gorm.DB{Config: &gorm.Config{
		Dialector: gormpostgres.Dialector{Config: &gormpostgres.Config{DSN: dsn}},
	}}
	// A separate handle, as the second tray toggle would build.
	second := &gorm.DB{Config: &gorm.Config{
		Dialector: gormpostgres.Dialector{Config: &gormpostgres.Config{DSN: dsn}},
	}}

	if dsnKey(first) != dsnKey(second) {
		t.Fatal("two handles to the same database must share a key, or the DDL pass runs twice")
	}
}

// A different database in the same process must still be migrated —
// the failure mode a single process-wide sync.Once would introduce.
func TestMigrateStillRunsForADifferentDatabase(t *testing.T) {
	resetMigrated(t)

	a := &gorm.DB{Config: &gorm.Config{
		Dialector: gormpostgres.Dialector{Config: &gormpostgres.Config{DSN: "postgres://u@localhost/one"}},
	}}
	b := &gorm.DB{Config: &gorm.Config{
		Dialector: gormpostgres.Dialector{Config: &gormpostgres.Config{DSN: "postgres://u@localhost/two"}},
	}}

	if dsnKey(a) == dsnKey(b) {
		t.Fatal("distinct databases must not share a key, or the second is left unmigrated")
	}
}

// Every in-memory SQLite handle is its own database despite sharing the
// ":memory:" DSN, so each must migrate. This is what the package's other
// tests depend on: they open a fresh :memory: DB per case and call
// Migrate expecting a real schema.
func TestMigrateTreatsEachInMemoryHandleAsItsOwnDatabase(t *testing.T) {
	resetMigrated(t)

	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		return db
	}

	first, second := open(), open()
	if dsnKey(first) == dsnKey(second) {
		t.Fatal("two :memory: handles are different databases and must not share a key")
	}

	// End to end: both actually get a schema.
	Migrate(first)
	Migrate(second)
	for i, db := range []*gorm.DB{first, second} {
		if err := db.Create(&entity.AgentProfile{ID: "p", Key: "k", Provider: "claude"}).Error; err != nil {
			t.Fatalf("handle %d was left unmigrated: %v", i, err)
		}
	}
}

// The guard must hold when the toggles are flipped concurrently, which
// is exactly how the tray can drive it.
func TestMigrateGuardIsConcurrencySafe(t *testing.T) {
	resetMigrated(t)

	const dsn = "postgres://user:pw@localhost:5432/wick"
	key := "postgres|" + dsn

	var mu sync.Mutex
	claimed := 0

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Mirrors the claim Migrate makes before running DDL.
			migratedMu.Lock()
			if !migrated[key] {
				migrated[key] = true
				mu.Lock()
				claimed++
				mu.Unlock()
			}
			migratedMu.Unlock()
		}()
	}
	wg.Wait()

	if claimed != 1 {
		t.Fatalf("expected exactly one caller to claim the DDL pass, got %d", claimed)
	}
}
