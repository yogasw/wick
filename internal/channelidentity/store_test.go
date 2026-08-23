package channelidentity

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	return db
}

func ident(userID, instance, external string) entity.UserChannelIdentity {
	return entity.UserChannelIdentity{
		UserID:         userID,
		ChannelType:    "slack",
		InstanceKey:    instance,
		ExternalUserID: external,
		DisplayName:    "Ada",
		EmailAtLink:    "ada@example.com",
	}
}

// TestLink_UpsertsInsteadOfDuplicating: Link runs on every resolved message,
// so it must refresh the existing row rather than pile up one per message.
func TestLink_UpsertsInsteadOfDuplicating(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := s.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	var n int64
	db.Model(&entity.UserChannelIdentity{}).Count(&n)
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}

// TestLink_SeparatesInstances is why InstanceKey is required: the same Slack
// user id in two workspaces is two different people, and collapsing them would
// let a notice go to the wrong workspace.
func TestLink_SeparatesInstances(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("link acme: %v", err)
	}
	if err := s.Link(ctx, ident("u2", "slack:other", "U123")); err != nil {
		t.Fatalf("link other: %v", err)
	}

	var n int64
	db.Model(&entity.UserChannelIdentity{}).Count(&n)
	if n != 2 {
		t.Fatalf("row count = %d, want 2 (same external id, different workspaces)", n)
	}
}

// TestLink_DefaultsBlankInstanceKey: a blank key would collide across bots, so
// it is never stored empty.
func TestLink_DefaultsBlankInstanceKey(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)

	if err := s.Link(context.Background(), ident("u1", "", "U123")); err != nil {
		t.Fatalf("link: %v", err)
	}
	var got entity.UserChannelIdentity
	if err := db.First(&got).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.InstanceKey == "" {
		t.Fatal("stored a blank instance key")
	}
}

// TestLink_IgnoresUnidentifiableInput: nothing to record is not an error, but
// it must not create a junk row either.
func TestLink_IgnoresUnidentifiableInput(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	for _, bad := range []entity.UserChannelIdentity{
		ident("", "slack:acme", "U123"),
		ident("u1", "slack:acme", ""),
		{UserID: "u1", ExternalUserID: "U1"}, // no channel type
	} {
		if err := s.Link(ctx, bad); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	var n int64
	db.Model(&entity.UserChannelIdentity{}).Count(&n)
	if n != 0 {
		t.Fatalf("row count = %d, want 0", n)
	}
}

// TestLink_DoesNotResurrectAPausedConnection: if a user paused a connection,
// messaging from it again must not silently re-enable delivery.
func TestLink_DoesNotResurrectAPausedConnection(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("link: %v", err)
	}
	var row entity.UserChannelIdentity
	db.First(&row)
	if err := s.SetPaused(ctx, "u1", row.ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Same sender messages again.
	if err := s.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	active, err := s.ActiveForUser(ctx, "u1")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 0 {
		t.Fatal("a re-link re-enabled a paused connection")
	}
}

// TestActiveForUser_ExcludesPaused is what makes the pause button honest: the
// notifier reads this, so a paused row must not be a delivery target.
func TestActiveForUser_ExcludesPaused(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:a", "U1")); err != nil {
		t.Fatalf("link a: %v", err)
	}
	if err := s.Link(ctx, ident("u1", "slack:b", "U2")); err != nil {
		t.Fatalf("link b: %v", err)
	}

	all, _ := s.ListForUser(ctx, "u1")
	if len(all) != 2 {
		t.Fatalf("list = %d, want 2", len(all))
	}
	if err := s.SetPaused(ctx, "u1", all[0].ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}

	active, _ := s.ActiveForUser(ctx, "u1")
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
	// ListForUser still shows both, so the UI can offer Resume.
	if all, _ = s.ListForUser(ctx, "u1"); len(all) != 2 {
		t.Fatalf("list after pause = %d, want 2", len(all))
	}
}

// TestSetPaused_ScopedToOwner: a caller must not be able to pause someone
// else's connection by guessing its id.
func TestSetPaused_ScopedToOwner(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	var row entity.UserChannelIdentity
	db.First(&row)

	if err := s.SetPaused(ctx, "attacker", row.ID, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	active, _ := s.ActiveForUser(ctx, "u1")
	if len(active) != 1 {
		t.Fatal("another user paused a connection they do not own")
	}
}

// TestSetPaused_Resume clears the pause.
func TestSetPaused_Resume(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	var row entity.UserChannelIdentity
	db.First(&row)

	_ = s.SetPaused(ctx, "u1", row.ID, true)
	if err := s.SetPaused(ctx, "u1", row.ID, false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	active, _ := s.ActiveForUser(ctx, "u1")
	if len(active) != 1 {
		t.Fatal("resume did not restore delivery")
	}
}

// TestLink_UpdatesLastSeen keeps the UI's "last active" column meaningful.
func TestLink_UpdatesLastSeen(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	ctx := context.Background()

	if err := s.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	s.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := s.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	var row entity.UserChannelIdentity
	db.First(&row)
	if row.LastSeenAt == nil || !row.LastSeenAt.After(base) {
		t.Fatalf("last_seen_at = %v, want advanced past %v", row.LastSeenAt, base)
	}
}
