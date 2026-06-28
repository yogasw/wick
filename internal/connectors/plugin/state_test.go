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

func newStateDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	postgres.Migrate(db)
	return db
}

func TestStateStoreDefaultsEnabled(t *testing.T) {
	s := NewStateStore(newStateDB(t))
	if !s.Enabled("never-seen") {
		t.Fatal("missing row should default to enabled")
	}
}

func TestStateStoreToggle(t *testing.T) {
	s := NewStateStore(newStateDB(t))
	if err := s.SetEnabled("slack", false); err != nil {
		t.Fatal(err)
	}
	if s.Enabled("slack") {
		t.Fatal("slack should be disabled")
	}
	if err := s.SetEnabled("slack", true); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled("slack") {
		t.Fatal("slack should be re-enabled")
	}
}

func TestStateStoreList(t *testing.T) {
	s := NewStateStore(newStateDB(t))
	_ = s.SetEnabled("a", false)
	_ = s.SetEnabled("b", true)
	m, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != false || m["b"] != true {
		t.Fatalf("unexpected list: %+v", m)
	}
}
