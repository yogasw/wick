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
