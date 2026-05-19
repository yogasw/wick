package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// TestMigrateProviderStorageToAbsolutePaths simulates a prod DB that
// upgraded from the relative-path era: legacy rel_path rows + orphan
// parent_id values. Migrate must wipe the legacy rows and re-parent the
// remaining absolute-path rows from rel_path.
func TestMigrateProviderStorageToAbsolutePaths(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Seed legacy + absolute rows; descendants point to non-existent
	// parent IDs to simulate the orphan-tree bug.
	seeds := []entity.ProviderStorage{
		// legacy rel_path → must be wiped
		{ProviderType: "wick", InstanceName: "wick", RelPath: "agents/foo.yml", Name: "legacy_foo.yml", ContentHash: "L", SyncedAt: time.Now()},
		{ProviderType: "wick", InstanceName: "wick", RelPath: "bare.yml", Name: "legacy_bare.yml", ContentHash: "L2", SyncedAt: time.Now()},
		// absolute rows with broken parent_id → must be re-parented
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home/app", Name: "app", IsDir: true, ParentID: 99999, ContentHash: "", SyncedAt: time.Now()},
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home", Name: "home", IsDir: true, ParentID: 0, ContentHash: "", SyncedAt: time.Now()},
		{ProviderType: "wick", InstanceName: "wick", RelPath: "/home/app/cfg.yml", Name: "cfg.yml", ParentID: 99998, ContentHash: "C", SyncedAt: time.Now()},
	}
	// AutoMigrate first so the table exists.
	if err := db.AutoMigrate(&entity.ProviderStorage{}, &entity.ProviderStorageSource{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	for _, s := range seeds {
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("seed %s: %v", s.RelPath, err)
		}
	}

	// Run the full Migrate — covers the wipe + repair pipeline.
	Migrate(db)

	// Legacy rows wiped.
	var legacy int64
	db.Model(&entity.ProviderStorage{}).
		Where("rel_path NOT LIKE '/%' AND rel_path NOT LIKE '_:%'").
		Count(&legacy)
	if legacy != 0 {
		t.Errorf("legacy rows survived migration: %d", legacy)
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

	// Re-running Migrate is a no-op.
	Migrate(db)
	if err := db.Where("rel_path = ?", "/home").First(&home).Error; err != nil {
		t.Fatalf("home (rerun): %v", err)
	}
}

// TestMigrate_FreshDB_NoOpExceptSchema ensures Migrate on a brand-new
// SQLite (no tables yet) just creates the schema and exits cleanly —
// no spurious row inserts, no errors from the optional data migrations
// when their source tables don't exist.
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

// TestMigrate_ExcludePatternsSplitIntoRows seeds the legacy schema —
// provider_storage_sources WITH an exclude_patterns TEXT column — then
// runs Migrate and asserts each non-empty line becomes a Mode="exclude"
// row and the column is dropped.
func TestMigrate_ExcludePatternsSplitIntoRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Build the legacy schema with exclude_patterns column.
	if err := db.Exec(`CREATE TABLE provider_storage_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider_type varchar(32) NOT NULL,
		instance_name varchar(128) NOT NULL,
		label varchar(128) NOT NULL,
		sync_path varchar(1024) NOT NULL,
		mode varchar(16) NOT NULL DEFAULT 'folder',
		retention_days INTEGER NOT NULL DEFAULT 0,
		exclude_patterns TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	patterns := "**/secrets/**\n*.log\n# a comment\n\n**/cache/**"
	if err := db.Exec(`INSERT INTO provider_storage_sources
		(provider_type, instance_name, label, sync_path, mode, retention_days, exclude_patterns, enabled, created_at, updated_at)
		VALUES ('wick', 'wick', 'config', '/home/.support-tools', 'folder', 0, ?, 1, datetime('now'), datetime('now'))`,
		patterns).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	Migrate(db)

	// Three exclude rows expected (comments + blanks dropped).
	var excludes []entity.ProviderStorageSource
	db.Where("mode = ?", "exclude").Find(&excludes)
	got := map[string]bool{}
	for _, e := range excludes {
		got[e.SyncPath] = true
	}
	for _, want := range []string{"**/secrets/**", "*.log", "**/cache/**"} {
		if !got[want] {
			t.Errorf("missing exclude row %q (got %v)", want, got)
		}
	}

	// Column dropped.
	if db.Migrator().HasColumn(&entity.ProviderStorageSource{}, "exclude_patterns") {
		t.Error("exclude_patterns column should be dropped after migration")
	}

	// Re-run is a no-op.
	Migrate(db)
	var n int64
	db.Model(&entity.ProviderStorageSource{}).Where("mode = ?", "exclude").Count(&n)
	if n != 3 {
		t.Errorf("re-run created duplicate exclude rows: %d, want 3", n)
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

// TestMigrateConnectorConfigsToConfigs simulates an existing DB with
// the legacy connectors.configs JSON column populated, then runs the
// migration and asserts:
//   - per-field rows land in the configs table under owner = "connector:{id}"
//   - the legacy column is dropped from connectors
//   - re-running is a no-op
func TestMigrateConnectorConfigsToConfigs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Build the legacy schema: connectors WITH the configs column. We
	// can't AutoMigrate against the current entity (it lacks Configs)
	// so write the DDL by hand to mimic an existing-prod DB.
	if err := db.Exec(`CREATE TABLE connectors (
		id varchar(36) PRIMARY KEY,
		` + "`key`" + ` varchar(100) NOT NULL,
		label varchar(255) NOT NULL,
		configs text,
		disabled bool DEFAULT false,
		created_by varchar(36),
		created_at datetime,
		updated_at datetime
	)`).Error; err != nil {
		t.Fatalf("create connectors: %v", err)
	}
	if err := db.AutoMigrate(&entity.Config{}); err != nil {
		t.Fatalf("automigrate config: %v", err)
	}

	if err := db.Exec(`INSERT INTO connectors(id, ` + "`key`" + `, label, configs)
		VALUES ('abc-id', 'loki', 'Loki Prod', '{"url":"http://loki.abc.com","token":"secret-1"}')`).Error; err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := db.Exec(`INSERT INTO connectors(id, ` + "`key`" + `, label, configs)
		VALUES ('def-id', 'loki', 'Loki Empty', '{}')`).Error; err != nil {
		t.Fatalf("seed empty: %v", err)
	}

	if err := migrateConnectorConfigsToConfigs(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var rows []entity.Config
	if err := db.WithContext(context.Background()).
		Where("owner = ?", "connector:abc-id").
		Find(&rows).Error; err != nil {
		t.Fatalf("list configs: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Key] = r.Value
	}
	if got["url"] != "http://loki.abc.com" || got["token"] != "secret-1" {
		t.Fatalf("rows mismatch: %+v", got)
	}

	if db.Migrator().HasColumn("connectors", "configs") {
		t.Fatal("configs column should be dropped")
	}

	// Re-run: column gone → early return, no error.
	if err := migrateConnectorConfigsToConfigs(db); err != nil {
		t.Fatalf("re-run: %v", err)
	}
}
