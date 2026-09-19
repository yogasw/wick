package admin

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// newAccessRepo is newTestRepo plus the users table — the access queries join
// it to drop unapproved accounts from every count.
func newAccessRepo(t *testing.T) *repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.Tag{}, &entity.ToolTag{}, &entity.UserTag{}, &entity.User{}); err != nil {
		t.Fatal(err)
	}
	return newRepo(db)
}

// seedAccess wires one restricted path (tag "support", two approved holders
// plus one unapproved) and leaves a second path untagged.
func seedAccess(t *testing.T, r *repo) (tagID string) {
	t.Helper()
	ctx := context.Background()
	tag := &entity.Tag{Name: "support", IsFilter: true}
	if err := r.db.WithContext(ctx).Create(tag).Error; err != nil {
		t.Fatal(err)
	}
	cosmetic := &entity.Tag{Name: "Messaging", IsFilter: false}
	if err := r.db.WithContext(ctx).Create(cosmetic).Error; err != nil {
		t.Fatal(err)
	}
	users := []entity.User{
		{ID: "u-hana", Email: "hana@example.com", Name: "Hana", Role: entity.RoleUser, Approved: true},
		{ID: "u-agung", Email: "agung@example.com", Name: "Agung", Role: entity.RoleUser, Approved: true},
		{ID: "u-pending", Email: "pending@example.com", Name: "Pending", Role: entity.RoleUser, Approved: false},
		{ID: "u-outsider", Email: "out@example.com", Name: "Outsider", Role: entity.RoleUser, Approved: true},
	}
	for i := range users {
		if err := r.db.WithContext(ctx).Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"u-hana", "u-agung", "u-pending"} {
		if err := r.db.WithContext(ctx).Create(&entity.UserTag{UserID: id, TagID: tag.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The restricted row, plus a cosmetic-only row that must still read as
	// public: a non-filter tag is a label, not a grant.
	for _, tt := range []entity.ToolTag{
		{ToolPath: "/connectors/c-restricted", TagID: tag.ID},
		{ToolPath: "/connectors/c-cosmetic", TagID: cosmetic.ID},
	} {
		if err := r.db.WithContext(ctx).Create(&tt).Error; err != nil {
			t.Fatal(err)
		}
	}
	return tag.ID
}

func TestAccessUserCountsSeparatesRestrictedFromPublic(t *testing.T) {
	r := newAccessRepo(t)
	seedAccess(t, r)
	ctx := context.Background()

	paths := []string{"/connectors/c-restricted", "/connectors/c-public", "/connectors/c-cosmetic"}
	counts, err := r.AccessUserCounts(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}

	n, ok := counts["/connectors/c-restricted"]
	if !ok {
		t.Fatal("a tagged path must appear in the result, otherwise it renders as public")
	}
	if n != 2 {
		t.Fatalf("restricted reach = %d, want 2 (the unapproved holder does not count)", n)
	}
	if _, ok := counts["/connectors/c-public"]; ok {
		t.Fatal("an untagged path must be absent — absence is how the caller reads 'public'")
	}
	if _, ok := counts["/connectors/c-cosmetic"]; ok {
		t.Fatal("a non-filter tag is a label, not a grant: the row is still public")
	}
}

// A row tagged with something nobody carries reaches zero people. It must
// report as restricted-with-zero, never as public — that is the misconfigured
// state the badge is meant to surface.
func TestAccessUserCountsTaggedButUnreachable(t *testing.T) {
	r := newAccessRepo(t)
	ctx := context.Background()
	orphan := &entity.Tag{Name: "nobody-has-this", IsFilter: true}
	if err := r.db.WithContext(ctx).Create(orphan).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.WithContext(ctx).Create(&entity.ToolTag{ToolPath: "/jobs/lonely", TagID: orphan.ID}).Error; err != nil {
		t.Fatal(err)
	}

	counts, err := r.AccessUserCounts(ctx, []string{"/jobs/lonely"})
	if err != nil {
		t.Fatal(err)
	}
	n, ok := counts["/jobs/lonely"]
	if !ok {
		t.Fatal("tagged-but-unreachable path fell out of the result and would render as public")
	}
	if n != 0 {
		t.Fatalf("reach = %d, want 0", n)
	}
}

func TestAccessDetailNamesTheTagThatLetEachUserIn(t *testing.T) {
	r := newAccessRepo(t)
	seedAccess(t, r)
	ctx := context.Background()

	detail, err := r.AccessDetail(ctx, "/connectors/c-restricted")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Public {
		t.Fatal("a tagged path reported itself public")
	}
	if len(detail.Users) != 2 {
		t.Fatalf("got %d users, want 2", len(detail.Users))
	}
	for _, u := range detail.Users {
		if len(u.ViaTags) != 1 || u.ViaTags[0] != "support" {
			t.Fatalf("user %s has via=%v, want [support]", u.Email, u.ViaTags)
		}
	}

	pub, err := r.AccessDetail(ctx, "/connectors/c-public")
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Public {
		t.Fatal("an untagged path must report as public")
	}
	if len(pub.Users) != 3 {
		t.Fatalf("public reach = %d users, want 3 approved", len(pub.Users))
	}
}

func TestTagUsageCountsAndDetail(t *testing.T) {
	r := newAccessRepo(t)
	tagID := seedAccess(t, r)
	ctx := context.Background()

	counts, err := r.TagUsageCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := counts[tagID]
	if got.UserCount != 2 {
		t.Fatalf("UserCount = %d, want 2", got.UserCount)
	}
	if got.ItemCount != 1 {
		t.Fatalf("ItemCount = %d, want 1", got.ItemCount)
	}

	detail, err := r.TagUsageDetail(ctx, tagID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TagName != "support" {
		t.Fatalf("TagName = %q", detail.TagName)
	}
	if len(detail.Items) != 1 || detail.Items[0].Path != "/connectors/c-restricted" {
		t.Fatalf("items = %+v", detail.Items)
	}
}

func TestAccessKindOfNamesEverySurface(t *testing.T) {
	cases := map[string]string{
		"/connectors/abc":         "Connectors",
		"/connector-accounts/abc": "Connected accounts",
		"/tools/convert-text":     "Tools",
		"/jobs/purge":             "Jobs",
		"/projects/p1":            "Projects",
		"/providers/claude/eng":   "Providers",
		"/data-tables/tasks":      "Data Tables",
		"/who-knows/x":            "who-knows",
	}
	for path, want := range cases {
		if got := accessKindOf(path); got != want {
			t.Errorf("accessKindOf(%q) = %q, want %q", path, got, want)
		}
	}
}
