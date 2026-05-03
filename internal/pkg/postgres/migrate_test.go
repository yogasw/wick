package postgres

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

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
