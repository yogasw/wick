package setup

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/entity"
)

func peruserDB(t *testing.T) *gorm.DB {
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

// fakeAuth satisfies rest.Authenticator so the REST composer registers.
type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, _ string) (string, error) { return "u", nil }

// TestTelegramRegistersKeyedPerUser asserts setup.Telegram creates one keyed
// instance per enabled owner row (mirrors the Slack behaviour).
func TestTelegramRegistersKeyedPerUser(t *testing.T) {
	db := peruserDB(t)
	store := agentchannels.NewDBStore(db)

	// Two users each with their own bot token.
	if err := agentchannels.SetChannelConfigKeyForUser(db, "telegram", "user-a", "bot_token", "111:AAA"); err != nil {
		t.Fatalf("seed user-a: %v", err)
	}
	if err := agentchannels.SetChannelConfigKeyForUser(db, "telegram", "user-b", "bot_token", "222:BBB"); err != nil {
		t.Fatalf("seed user-b: %v", err)
	}

	reg := agentchannels.NewRegistry()
	Telegram(reg, store, nil)

	if !reg.HasKey("telegram:user-a") {
		t.Errorf("expected keyed instance telegram:user-a")
	}
	if !reg.HasKey("telegram:user-b") {
		t.Errorf("expected keyed instance telegram:user-b")
	}
}

// TestRestRegistersKeyedPerUser asserts setup.Rest creates one keyed instance
// per enabled owner row.
func TestRestRegistersKeyedPerUser(t *testing.T) {
	db := peruserDB(t)
	store := agentchannels.NewDBStore(db)

	if err := agentchannels.SetChannelConfigKeyForUser(db, "rest", "user-a", "enabled", "true"); err != nil {
		t.Fatalf("seed user-a: %v", err)
	}
	if err := agentchannels.SetChannelConfigKeyForUser(db, "rest", "user-b", "enabled", "true"); err != nil {
		t.Fatalf("seed user-b: %v", err)
	}

	reg := agentchannels.NewRegistry()
	Rest(reg, store, nil, fakeAuth{})

	if !reg.HasKey("rest:user-a") {
		t.Errorf("expected keyed instance rest:user-a")
	}
	if !reg.HasKey("rest:user-b") {
		t.Errorf("expected keyed instance rest:user-b")
	}
}
