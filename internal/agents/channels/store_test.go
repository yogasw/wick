package channels

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&entity.AgentChannel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestEnsureChannelForUser(t *testing.T) {
	db := testDB(t)

	if err := EnsureChannelForUser(db, "slack", ""); err != nil {
		t.Fatalf("EnsureChannelForUser owner: %v", err)
	}
	if err := EnsureChannelForUser(db, "slack", "user-a"); err != nil {
		t.Fatalf("EnsureChannelForUser user-a: %v", err)
	}
	if err := EnsureChannelForUser(db, "slack", "user-a"); err != nil {
		t.Fatalf("EnsureChannelForUser idempotent: %v", err)
	}

	var count int64
	db.Model(&entity.AgentChannel{}).Where("type = ?", "slack").Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
}

func TestSetAndGetChannelConfigKeyForUser(t *testing.T) {
	db := testDB(t)

	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-test-123"); err != nil {
		t.Fatalf("set config: %v", err)
	}

	m, err := GetChannelConfigMapForUser(db, "slack", "user-a")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if m["bot_token"] != "xoxb-test-123" {
		t.Fatalf("expected bot_token=xoxb-test-123, got %q", m["bot_token"])
	}
}

func TestListChannelOwners(t *testing.T) {
	db := testDB(t)

	owners, err := ListChannelOwners(db, "slack")
	if err != nil {
		t.Fatalf("list owners: %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("expected 0, got %d", len(owners))
	}

	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-abc"); err != nil {
		t.Fatalf("set: %v", err)
	}

	owners, err = ListChannelOwners(db, "slack")
	if err != nil {
		t.Fatalf("list owners 2: %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("expected 1 enabled owner, got %d", len(owners))
	}
	if owners[0] == nil || *owners[0] != "user-a" {
		t.Fatalf("expected user-a, got %v", owners[0])
	}
}

func TestOwnerRowIsolation(t *testing.T) {
	db := testDB(t)

	if err := SetChannelConfigKeyForUser(db, "slack", "", "bot_token", "xoxb-owner"); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if err := SetChannelConfigKeyForUser(db, "slack", "user-a", "bot_token", "xoxb-user-a"); err != nil {
		t.Fatalf("set user-a: %v", err)
	}

	ownerMap, _ := GetChannelConfigMapForUser(db, "slack", "")
	userMap, _ := GetChannelConfigMapForUser(db, "slack", "user-a")

	if ownerMap["bot_token"] != "xoxb-owner" {
		t.Fatalf("owner isolation broken: got %q", ownerMap["bot_token"])
	}
	if userMap["bot_token"] != "xoxb-user-a" {
		t.Fatalf("user-a isolation broken: got %q", userMap["bot_token"])
	}
}
