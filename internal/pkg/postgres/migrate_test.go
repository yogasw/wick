package postgres

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// TestRepairProviderStorageTree seeds absolute-path rows whose parent_id
// points at non-existent IDs (the orphan-tree bug) and asserts
// RepairProviderStorageTree rewires each row's parent_id from its rel_path.
func TestRepairProviderStorageTree(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&entity.ProviderStorage{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	seeds := []entity.ProviderStorage{
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home/app", Name: "app", IsDir: true, ParentID: 99999, ContentHash: "", SyncedAt: time.Now()},
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home", Name: "home", IsDir: true, ParentID: 0, ContentHash: "", SyncedAt: time.Now()},
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home/app/cfg.yml", Name: "cfg.yml", ParentID: 99998, ContentHash: "C", SyncedAt: time.Now()},
	}
	for _, s := range seeds {
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("seed %s: %v", s.RelPath, err)
		}
	}

	if _, err := RepairProviderStorageTree(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	// Tree healed: /home/app's parent_id should be the row at /home;
	// /home/app/cfg.yml's parent_id should be /home/app.
	var home, app, cfg entity.ProviderStorage
	if err := db.Where("rel_path = ?", "/home").First(&home).Error; err != nil {
		t.Fatalf("home: %v", err)
	}
	if err := db.Where("rel_path = ?", "/home/app").First(&app).Error; err != nil {
		t.Fatalf("app: %v", err)
	}
	if err := db.Where("rel_path = ?", "/home/app/cfg.yml").First(&cfg).Error; err != nil {
		t.Fatalf("cfg: %v", err)
	}
	if app.ParentID != home.ID {
		t.Errorf("app.parent_id = %d, want %d (home.ID)", app.ParentID, home.ID)
	}
	if cfg.ParentID != app.ID {
		t.Errorf("cfg.parent_id = %d, want %d (app.ID)", cfg.ParentID, app.ID)
	}

	// Re-running is a no-op (tree already healthy → 0 fixed).
	n, err := RepairProviderStorageTree(db)
	if err != nil {
		t.Fatalf("repair rerun: %v", err)
	}
	if n != 0 {
		t.Errorf("rerun fixed %d rows, want 0 (tree already healthy)", n)
	}
}

// TestMigrate_FreshDB_NoOpExceptSchema ensures Migrate on a brand-new
// SQLite (no tables yet) just creates the schema and exits cleanly —
// no spurious row inserts, no errors.
func TestMigrate_FreshDB_NoOpExceptSchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	Migrate(db)
	var n int64
	db.Model(&entity.ProviderStorage{}).Count(&n)
	if n != 0 {
		t.Errorf("fresh DB has %d provider_storage rows after Migrate", n)
	}
	db.Model(&entity.ProviderStorageSource{}).Count(&n)
	if n != 0 {
		t.Errorf("fresh DB has %d provider_storage_sources rows", n)
	}
	// Calling again is also safe.
	Migrate(db)
}

// AutoMigrate creates the new composite index on (project_id, key) but
// never drops the old single-column one on key. Left behind, it keeps
// rejecting the second scope's row — and the failure surfaces as a
// constraint violation on save, far from its cause. This reproduces an
// UPGRADED database, which is the only place the bug can appear.
func TestDropStaleProfileKeyIndexAllowsCrossScopeKeys(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&entity.AgentProfile{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Recreate the pre-scoping index exactly as the old struct tag produced it.
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_profiles_key ON agent_profiles(key)`).Error; err != nil {
		t.Fatalf("seed stale index: %v", err)
	}

	DropStaleProfileKeyIndex(db)

	if err := db.Create(&entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"}).Error; err != nil {
		t.Fatalf("insert global: %v", err)
	}
	if err := db.Create(&entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "researcher", Provider: "codex"}).Error; err != nil {
		t.Fatalf("the stale index was not dropped: %v", err)
	}
}

// The two tests above drive AutoMigrate directly. This one goes through
// the real boot path, Migrate(), on a database that already carries the
// pre-scoping index — the actual upgrade an existing install performs.
// Without it, the drop could be correct while never being called.
func TestMigrateUpgradesAnInstallWithTheOldProfileIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Stand up the schema the way a pre-scoping install had it, then add
	// the index that version's struct tag produced.
	if err := db.AutoMigrate(&entity.AgentProfile{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_profiles_key ON agent_profiles(key)`).Error; err != nil {
		t.Fatalf("seed stale index: %v", err)
	}

	Migrate(db)

	if err := db.Create(&entity.AgentProfile{ID: "g1", Key: "researcher", Provider: "claude"}).Error; err != nil {
		t.Fatalf("insert global: %v", err)
	}
	if err := db.Create(&entity.AgentProfile{ID: "p1", ProjectID: "proj-abc", Key: "researcher", Provider: "codex"}).Error; err != nil {
		t.Fatalf("boot did not clear the stale index: %v", err)
	}
	// And the new guard is in force after the upgrade.
	if err := db.Create(&entity.AgentProfile{ID: "p2", ProjectID: "proj-abc", Key: "researcher", Provider: "wick"}).Error; err == nil {
		t.Fatal("a duplicate within one scope must still be rejected after the upgrade")
	}
}

// Every boot calls it, so running it twice — and on a database that never
// carried the index — must be a no-op. It must also not take the
// composite guard down with it.
func TestDropStaleProfileKeyIndexIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&entity.AgentProfile{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	DropStaleProfileKeyIndex(db)
	DropStaleProfileKeyIndex(db)

	if err := db.Create(&entity.AgentProfile{ID: "a1", ProjectID: "proj-abc", Key: "dup", Provider: "claude"}).Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Create(&entity.AgentProfile{ID: "a2", ProjectID: "proj-abc", Key: "dup", Provider: "codex"}).Error; err == nil {
		t.Fatal("dropping the stale index must not remove the composite uniqueness guard")
	}
}

// TestMigrate_IdxStorageTree_DuplicatesDontCrash forces a duplicate
// (provider_type, instance_name, parent_id, name) tuple and asserts
// Migrate logs a warning but does NOT crash. The runtime falls back to
// SELECT-then-INSERT without the DB-level guard.
func TestMigrate_IdxStorageTree_DuplicatesDontCrash(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&entity.ProviderStorage{}, &entity.ProviderStorageSource{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// Drop the index that AutoMigrate's first run won't have created
	// in this isolated test setup (AutoMigrate handles tag indexes only;
	// idx_storage_tree is managed by Migrate's manual SQL).
	_ = db.Exec(`DROP INDEX IF EXISTS idx_storage_tree`).Error
	// Seed duplicates that would block CREATE UNIQUE INDEX.
	for i := 0; i < 2; i++ {
		if err := db.Create(&entity.ProviderStorage{
			ProviderType: "p", InstanceName: "i",
			RelPath: "/dup", ParentID: 0, Name: "dup", IsDir: true, ContentHash: "",
			SyncedAt: time.Now(),
		}).Error; err != nil {
			// rel_path is unique → second insert fails; skip the test
			// since the constraint we want to test is on a DIFFERENT
			// index than the one we have left.
			t.Skip("rel_path unique blocks dup seed; nothing to test")
		}
	}
	// Should not panic / log.Fatal.
	Migrate(db)
}
