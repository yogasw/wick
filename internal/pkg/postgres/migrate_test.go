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
