package tags

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newTagsSQLite(t *testing.T) *gorm.DB {
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

// A connector row carrying an owner:<id> tag is "private"; a row with no
// tag is not. This is the shared primitive behind the manager list's 🔒
// Private badge and the workflow palette's "Mine" badge.
func TestPrivateResourceIDs(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newTagsSQLite(t))

	// "priv" is owned by u-1 (owner tag) → private.
	// "pub" has no tag → not private.
	if err := svc.CreateOwnerTag(ctx, "priv", "u-1"); err != nil {
		t.Fatalf("CreateOwnerTag: %v", err)
	}

	paths := []string{"/connectors/priv", "/connectors/pub"}
	got, err := svc.PrivateResourceIDs(ctx, paths)
	if err != nil {
		t.Fatalf("PrivateResourceIDs: %v", err)
	}

	if !got["priv"] {
		t.Errorf("expected priv to be private, got %v", got)
	}
	if got["pub"] {
		t.Errorf("expected pub NOT to be private, got %v", got)
	}
	if len(got) != 1 {
		t.Errorf("expected exactly 1 private id, got %d: %v", len(got), got)
	}
}

func TestPrivateResourceIDsEmpty(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newTagsSQLite(t))

	got, err := svc.PrivateResourceIDs(ctx, nil)
	if err != nil {
		t.Fatalf("PrivateResourceIDs(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}
