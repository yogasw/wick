package login

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newLoginSQLite(t *testing.T) *gorm.DB {
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

// tagResource creates a filter tag `tagName` and attaches it to `resourcePath`
// in tool_tags, returning the tag ID a user must carry to reach the resource.
func tagResource(t *testing.T, db *gorm.DB, resourcePath, tagName string) string {
	t.Helper()
	tag := entity.Tag{Name: tagName, IsFilter: true}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("create tag %q: %v", tagName, err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: resourcePath, TagID: tag.ID}).Error; err != nil {
		t.Fatalf("link tool_tag %q: %v", resourcePath, err)
	}
	return tag.ID
}

func TestAnyTagMatch(t *testing.T) {
	tests := []struct {
		name   string
		user   []string
		filter []string
		want   bool
	}{
		{"empty both", nil, nil, false},
		{"empty user", nil, []string{"a"}, false},
		{"empty filter", []string{"a"}, nil, false},
		{"no overlap", []string{"a", "b"}, []string{"c"}, false},
		{"single overlap", []string{"a", "b"}, []string{"b"}, true},
		{"overlap first", []string{"x"}, []string{"x", "y"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := anyTagMatch(tt.user, tt.filter); got != tt.want {
				t.Fatalf("anyTagMatch(%v,%v) = %v, want %v", tt.user, tt.filter, got, tt.want)
			}
		})
	}
}

// TestCanAccessSharedResource pins the rule that broke the sidebar: for a
// project / data table, a NON-owner reaches it ONLY through a matching filter
// tag. Crucially, an UNTAGGED resource is NOT public here (that is the bug that
// made every project show for every user) — untagged returns false, leaving
// owner/admin access to the caller.
func TestCanAccessSharedResource(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")

	const path = "/projects/p1"
	tagID := tagResource(t, db, path, "team-x")

	approved := &entity.User{ID: "u1", Email: "u1@abc.com", Name: "U1", Approved: true, Role: entity.RoleUser}

	t.Run("untagged resource is not shared (stays owner-private)", func(t *testing.T) {
		ctx := WithUser(context.Background(), approved, []string{tagID})
		if svc.CanAccessSharedResource(ctx, approved, "/projects/untagged") {
			t.Fatal("untagged resource must NOT be reachable via tag share; owner/admin handled by caller")
		}
	})

	t.Run("user carrying the filter tag is admitted", func(t *testing.T) {
		ctx := WithUser(context.Background(), approved, []string{tagID})
		if !svc.CanAccessSharedResource(ctx, approved, path) {
			t.Fatal("user with matching filter tag should reach the shared resource")
		}
	})

	t.Run("user without the filter tag is denied", func(t *testing.T) {
		ctx := WithUser(context.Background(), approved, []string{"some-other-tag"})
		if svc.CanAccessSharedResource(ctx, approved, path) {
			t.Fatal("user lacking the filter tag must be denied")
		}
	})

	t.Run("unapproved user is denied even with the tag", func(t *testing.T) {
		unapproved := &entity.User{ID: "u2", Email: "u2@abc.com", Name: "U2", Approved: false, Role: entity.RoleUser}
		ctx := WithUser(context.Background(), unapproved, []string{tagID})
		if svc.CanAccessSharedResource(ctx, unapproved, path) {
			t.Fatal("unapproved user must be denied")
		}
	})

	t.Run("nil user is denied", func(t *testing.T) {
		if svc.CanAccessSharedResource(context.Background(), nil, path) {
			t.Fatal("nil user must be denied")
		}
	})
}

// TestCanAccessToolVsSharedResourceUntagged locks in the exact divergence: an
// untagged tool is OPEN (CanAccessTool=true) while an untagged shared resource
// is CLOSED (CanAccessSharedResource=false). If someone "unifies" these two,
// this test fails and reminds them why the split exists.
func TestCanAccessToolVsSharedResourceUntagged(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")

	u := &entity.User{ID: "u1", Email: "u1@abc.com", Name: "U1", Approved: true, Role: entity.RoleUser}
	ctx := WithUser(context.Background(), u, nil)

	if !svc.CanAccessTool(ctx, u, "/tools/untagged", entity.VisibilityPrivate) {
		t.Fatal("untagged private tool should be open to approved users (untagged = public)")
	}
	if svc.CanAccessSharedResource(ctx, u, "/projects/untagged") {
		t.Fatal("untagged project must NOT be open (owner-private, not public)")
	}
}

// grantUserTag links an existing tag to a user the way /admin does.
func grantUserTag(t *testing.T, db *gorm.DB, userID, tagID string) {
	t.Helper()
	if err := db.Create(&entity.UserTag{UserID: userID, TagID: tagID}).Error; err != nil {
		t.Fatalf("grant tag: %v", err)
	}
}

// TestSharedResourceSeesTagGrantedAfterLogin is the bug behind "I shared the
// project with them and it still does not show up": the context's tag set comes
// from the session cookie, minted at LOGIN, so a grant made afterwards was
// invisible until the person signed out and back in. The check has to read the
// live set.
func TestSharedResourceSeesTagGrantedAfterLogin(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	path := "/projects/p-shared"
	tagID := tagResource(t, db, path, "team-y")

	u := &entity.User{ID: "u-late", Email: "late@abc.com", Name: "Late", Approved: true, Role: entity.RoleUser}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Signed in BEFORE the grant: the cookie carries no tags at all.
	ctx := WithUser(context.Background(), u, nil)
	if svc.CanAccessSharedResource(ctx, u, path) {
		t.Fatal("user reached the project before being granted anything")
	}

	grantUserTag(t, db, u.ID, tagID)
	svc.tagCache.Delete(u.ID) // the cache's TTL is not what this test is about

	if !svc.CanAccessSharedResource(ctx, u, path) {
		t.Fatal("a tag granted after login must take effect without re-login")
	}
}

// The explicit set a caller hands in still counts on its own — MCP bearer
// contexts pass their tags directly and have no user_tags rows to read.
func TestSharedResourceKeepsExplicitContextTags(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	path := "/projects/p-ctx"
	tagID := tagResource(t, db, path, "team-z")

	u := &entity.User{ID: "u-ctx", Email: "ctx@abc.com", Name: "Ctx", Approved: true, Role: entity.RoleUser}
	ctx := WithUser(context.Background(), u, []string{tagID})
	if !svc.CanAccessSharedResource(ctx, u, path) {
		t.Fatal("tags handed in explicitly should still admit the caller")
	}
}

// TestSharedResourceForAnotherUserIgnoresViewerTags: "can the workflow's owner
// reach this project?" is asked inside the VIEWER's request. Answering it with
// the viewer's cookie tags would report the viewer's reach as the owner's.
func TestSharedResourceForAnotherUserIgnoresViewerTags(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	path := "/projects/p-other"
	tagID := tagResource(t, db, path, "team-w")

	viewer := &entity.User{ID: "u-viewer", Email: "v@abc.com", Name: "V", Approved: true, Role: entity.RoleUser}
	owner := &entity.User{ID: "u-owner", Email: "o@abc.com", Name: "O", Approved: true, Role: entity.RoleUser}
	for _, u := range []*entity.User{viewer, owner} {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	// The viewer carries the tag; the owner does not.
	ctx := WithUser(context.Background(), viewer, []string{tagID})

	if !svc.CanAccessSharedResource(ctx, viewer, path) {
		t.Fatal("viewer should reach the project they carry the tag for")
	}
	if svc.CanAccessSharedResource(ctx, owner, path) {
		t.Fatal("the owner's access was answered with the viewer's tags")
	}
}
