package login

import (
	"context"
	"sort"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"gorm.io/gorm"
)

func seedTag(t *testing.T, db *gorm.DB, name string, system bool) string {
	t.Helper()
	tag := entity.Tag{Name: name, IsFilter: true, IsSystem: system}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("create tag %s: %v", name, err)
	}
	return tag.ID
}

func userTagIDs(t *testing.T, db *gorm.DB, userID string) []string {
	t.Helper()
	var ids []string
	db.Model(&entity.UserTag{}).Where("user_id = ?", userID).Pluck("tag_id", &ids)
	sort.Strings(ids)
	return ids
}

// An account approved by the domain policy gets the configured tags at that
// moment, and only then: a later sign-in must not re-add a tag an admin took
// away.
func TestUpsertSSOUser_AutoApproveTags(t *testing.T) {
	ctx := context.Background()
	db := newLoginSQLite(t)
	svc := NewService(db, "boss@acme.test")
	if _, err := svc.UpsertUser(ctx, "boss@acme.test", "Boss", ""); err != nil {
		t.Fatal(err)
	}
	team := seedTag(t, db, "team-a", false)
	sys := seedTag(t, db, "sys-only", true)

	u, err := svc.UpsertSSOUser(ctx, "dev@acme.test", "Dev", "", true, true, []string{team, sys, "missing-id"})
	if err != nil || !u.Approved {
		t.Fatalf("auto-approve: %+v, %v", u, err)
	}
	if got := userTagIDs(t, db, u.ID); len(got) != 1 || got[0] != team {
		t.Fatalf("tags = %v, want only %s (system and unknown skipped)", got, team)
	}

	db.Where("user_id = ?", u.ID).Delete(&entity.UserTag{})
	if _, err := svc.UpsertSSOUser(ctx, "dev@acme.test", "Dev", "", true, true, []string{team}); err != nil {
		t.Fatal(err)
	}
	if got := userTagIDs(t, db, u.ID); len(got) != 0 {
		t.Fatalf("already-approved sign-in re-added tags: %v", got)
	}

	// A pending sign-in (domain not listed) gets nothing.
	p, err := svc.UpsertSSOUser(ctx, "guest@other.test", "Guest", "", true, false, []string{team})
	if err != nil || p.Approved {
		t.Fatalf("pending: %+v, %v", p, err)
	}
	if got := userTagIDs(t, db, p.ID); len(got) != 0 {
		t.Fatalf("pending account got tags: %v", got)
	}
}

// A channel-registered account is approved once with its tags; the second
// call reports no change and adds nothing.
func TestApproveChannelUser_TagsOnce(t *testing.T) {
	ctx := context.Background()
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	team := seedTag(t, db, "team-a", false)

	id, err := svc.RegisterChannelUser(ctx, "dev@acme.test", "Dev", "slack")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := svc.ApproveChannelUser(ctx, id, []string{team})
	if err != nil || !changed {
		t.Fatalf("first approve: changed=%v err=%v", changed, err)
	}
	u, _ := svc.GetUserByID(ctx, id)
	if !u.Approved || u.IsAdmin() {
		t.Fatalf("user = %+v, want approved plain user", u)
	}
	if got := userTagIDs(t, db, id); len(got) != 1 || got[0] != team {
		t.Fatalf("tags = %v, want [%s]", got, team)
	}

	db.Where("user_id = ?", id).Delete(&entity.UserTag{})
	changed, err = svc.ApproveChannelUser(ctx, id, []string{team})
	if err != nil || changed {
		t.Fatalf("second approve: changed=%v err=%v, want no change", changed, err)
	}
	if got := userTagIDs(t, db, id); len(got) != 0 {
		t.Fatalf("second approve re-added tags: %v", got)
	}
}
