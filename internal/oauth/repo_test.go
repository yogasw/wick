package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newGrantTestRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.OAuthClient{}, &entity.OAuthToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&entity.OAuthClient{
		ClientID: "wick_app_test",
		Name:     "Claude Code (support-assistant)",
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	return NewRepo(db)
}

func mustCreateToken(t *testing.T, r *Repo, tok *entity.OAuthToken) *entity.OAuthToken {
	t.Helper()
	if err := r.CreateToken(context.Background(), tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return tok
}

// seedUsedThenExpiredGrant reproduces the shape every real grant has
// an hour after it is minted: the access token that carried the usage
// stamp has expired, and only the long-lived refresh token is still
// active.
func seedUsedThenExpiredGrant(t *testing.T, r *Repo, userID string) time.Time {
	t.Helper()
	granted := time.Now().Add(-72 * time.Hour)
	used := granted.Add(30 * time.Minute)

	mustCreateToken(t, r, &entity.OAuthToken{
		TokenHash:  "access-hash-" + userID,
		Kind:       "access",
		ClientID:   "wick_app_test",
		UserID:     userID,
		ExpiresAt:  granted.Add(AccessTokenTTL), // expired two days ago
		LastUsedAt: &used,
	})
	mustCreateToken(t, r, &entity.OAuthToken{
		TokenHash: "refresh-hash-" + userID,
		Kind:      "refresh",
		ClientID:  "wick_app_test",
		UserID:    userID,
		ExpiresAt: granted.Add(RefreshTokenTTL), // still active
	})
	return used
}

func TestListGrantsByUserKeepsLastUsedAfterAccessTokenExpires(t *testing.T) {
	r := newGrantTestRepo(t)
	used := seedUsedThenExpiredGrant(t, r, "user-1")

	grants, err := r.ListGrantsByUser(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	g := grants[0]
	if g.LastUsedAt == nil {
		t.Fatal(`last used reported "never" for a grant whose access token was used before it expired`)
	}
	if diff := g.LastUsedAt.Sub(used); diff > time.Second || diff < -time.Second {
		t.Errorf("last used = %v, want %v", g.LastUsedAt, used)
	}
	if g.TokenCount != 1 {
		t.Errorf("token count = %d, want 1 (only the refresh token is still active)", g.TokenCount)
	}
}

func TestListAllGrantsKeepsLastUsedAfterAccessTokenExpires(t *testing.T) {
	r := newGrantTestRepo(t)
	usedA := seedUsedThenExpiredGrant(t, r, "user-a")
	usedB := seedUsedThenExpiredGrant(t, r, "user-b")

	grants, err := r.ListAllGrants(context.Background())
	if err != nil {
		t.Fatalf("list all grants: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("want 2 grants, got %d", len(grants))
	}
	want := map[string]time.Time{"user-a": usedA, "user-b": usedB}
	for _, g := range grants {
		exp, ok := want[g.UserID]
		if !ok {
			t.Fatalf("unexpected user %q", g.UserID)
		}
		if g.LastUsedAt == nil {
			t.Fatalf(`last used reported "never" for %s`, g.UserID)
		}
		if diff := g.LastUsedAt.Sub(exp); diff > time.Second || diff < -time.Second {
			t.Errorf("%s last used = %v, want %v", g.UserID, g.LastUsedAt, exp)
		}
		if g.TokenCount != 1 {
			t.Errorf("%s token count = %d, want 1", g.UserID, g.TokenCount)
		}
	}
}

// A revoked token still counts as evidence of use — refresh rotation
// revokes the spent refresh row, and disconnect revokes everything.
func TestGrantLastUsedSurvivesRevocation(t *testing.T) {
	r := newGrantTestRepo(t)
	ctx := context.Background()

	rotatedUse := time.Now().Add(-5 * time.Hour)
	spent := mustCreateToken(t, r, &entity.OAuthToken{
		TokenHash:  "old-refresh",
		Kind:       "refresh",
		ClientID:   "wick_app_test",
		UserID:     "user-1",
		ExpiresAt:  time.Now().Add(RefreshTokenTTL),
		LastUsedAt: &rotatedUse,
	})
	if err := r.Revoke(ctx, spent.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	mustCreateToken(t, r, &entity.OAuthToken{
		TokenHash:     "new-refresh",
		Kind:          "refresh",
		ClientID:      "wick_app_test",
		UserID:        "user-1",
		ParentTokenID: &spent.ID,
		ExpiresAt:     time.Now().Add(RefreshTokenTTL),
	})

	grants, err := r.ListGrantsByUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	if grants[0].LastUsedAt == nil {
		t.Fatal("last used lost when the refresh token it was stamped on was rotated out")
	}
	if grants[0].TokenCount != 1 {
		t.Errorf("token count = %d, want 1 (the revoked refresh must not be counted)", grants[0].TokenCount)
	}
}

// A grant nobody has ever authenticated with must still read "never".
func TestGrantNeverUsedStaysNil(t *testing.T) {
	r := newGrantTestRepo(t)
	mustCreateToken(t, r, &entity.OAuthToken{
		TokenHash: "fresh-refresh",
		Kind:      "refresh",
		ClientID:  "wick_app_test",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(RefreshTokenTTL),
	})

	grants, err := r.ListGrantsByUser(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	if grants[0].LastUsedAt != nil {
		t.Errorf("last used = %v, want nil for an unused grant", grants[0].LastUsedAt)
	}
}
