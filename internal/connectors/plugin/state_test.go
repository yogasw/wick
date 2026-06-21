package plugin

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/pkg/postgres"
)

func TestMigrateCreatesPluginStates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	postgres.Migrate(db)
	if !db.Migrator().HasTable("plugin_states") {
		t.Fatal("migrate should create plugin_states table")
	}
}
