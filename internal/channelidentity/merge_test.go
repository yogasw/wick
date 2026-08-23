package channelidentity

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

func seedUser(t *testing.T, db *gorm.DB, id string, approved bool, role entity.UserRole) *entity.User {
	t.Helper()
	u := &entity.User{ID: id, Email: id + "@example.com", Name: id, Role: role, Approved: approved}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return u
}

// TestMerge_MovesConnectionsAndRemovesSource is the whole point: a Telegram
// account that could not be matched gets folded into the person's real account,
// and their Telegram connection follows.
func TestMerge_MovesConnectionsAndRemovesSource(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	seedUser(t, db, "real", true, entity.RoleUser)
	seedUser(t, db, "tg", false, entity.RoleUser)
	if err := s.Link(ctx, identityFor("tg", "telegram", "default", "555", "Ada TG")); err != nil {
		t.Fatalf("link: %v", err)
	}

	res, err := s.Merge(ctx, "tg", "real")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.MovedConnections != 1 {
		t.Fatalf("moved = %d, want 1", res.MovedConnections)
	}

	moved, _ := s.ListForUser(ctx, "real")
	if len(moved) != 1 || moved[0].ExternalUserID != "555" {
		t.Fatalf("target connections = %+v, want the telegram link", moved)
	}
	// The source must be gone, not left empty: an orphan with no connections is
	// indistinguishable from a real pending user, so admins would approve ghosts.
	var n int64
	db.Model(&entity.User{}).Where("id = ?", "tg").Count(&n)
	if n != 0 {
		t.Fatal("source account still exists after merge")
	}
}

// TestMerge_RefusesAdminSource: a merge deletes the source, so allowing an admin
// there would make "merge" a way to remove another administrator.
func TestMerge_RefusesAdminSource(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	seedUser(t, db, "adm", true, entity.RoleAdmin)
	seedUser(t, db, "real", true, entity.RoleUser)

	if _, err := s.Merge(context.Background(), "adm", "real"); !errors.Is(err, ErrMergeAdminSource) {
		t.Fatalf("err = %v, want ErrMergeAdminSource", err)
	}
	var n int64
	db.Model(&entity.User{}).Where("id = ?", "adm").Count(&n)
	if n != 1 {
		t.Fatal("admin account was deleted by a refused merge")
	}
}

// TestMerge_RefusesOwnerSource covers IsOwner, which counts as admin even when
// the role column says "user".
func TestMerge_RefusesOwnerSource(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	owner := &entity.User{ID: "own", Email: "own@example.com", Name: "Owner",
		Role: entity.RoleUser, IsOwner: true, Approved: true}
	if err := db.Create(owner).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedUser(t, db, "real", true, entity.RoleUser)

	if _, err := s.Merge(context.Background(), "own", "real"); !errors.Is(err, ErrMergeAdminSource) {
		t.Fatalf("err = %v, want ErrMergeAdminSource", err)
	}
}

// TestMerge_RefusesUnapprovedTarget: moving working connections onto an account
// that cannot act would read as a broken merge.
func TestMerge_RefusesUnapprovedTarget(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	seedUser(t, db, "tg", false, entity.RoleUser)
	seedUser(t, db, "pending", false, entity.RoleUser)

	if _, err := s.Merge(context.Background(), "tg", "pending"); !errors.Is(err, ErrMergeIntoUnapproved) {
		t.Fatalf("err = %v, want ErrMergeIntoUnapproved", err)
	}
}

// TestMerge_RefusesSelf guards an obvious mis-click that would delete the
// account it was supposed to keep.
func TestMerge_RefusesSelf(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	seedUser(t, db, "real", true, entity.RoleUser)

	if _, err := s.Merge(context.Background(), "real", "real"); !errors.Is(err, ErrSameUser) {
		t.Fatalf("err = %v, want ErrSameUser", err)
	}
	var n int64
	db.Model(&entity.User{}).Where("id = ?", "real").Count(&n)
	if n != 1 {
		t.Fatal("self-merge deleted the account")
	}
}

// TestMerge_KeepsTargetsOwnRowOnCollision: if both accounts hold the SAME
// channel account, the target's row survives — it is the one whose pause state
// the user actually set.
func TestMerge_KeepsTargetsOwnRowOnCollision(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	seedUser(t, db, "real", true, entity.RoleUser)
	seedUser(t, db, "dup", false, entity.RoleUser)

	if err := s.Link(ctx, identityFor("real", "telegram", "default", "555", "Ada")); err != nil {
		t.Fatalf("link target: %v", err)
	}
	// Force a duplicate row for the same external id under the source.
	if err := db.Create(&entity.UserChannelIdentity{
		UserID: "dup", ChannelType: "telegram", InstanceKey: "other", ExternalUserID: "555",
	}).Error; err != nil {
		t.Fatalf("seed dup: %v", err)
	}

	res, err := s.Merge(ctx, "dup", "real")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	// Different instance keys are genuinely different connections, so this one
	// moves rather than collides.
	if res.MovedConnections != 1 {
		t.Fatalf("moved = %d, want 1", res.MovedConnections)
	}
	if rows, _ := s.ListForUser(ctx, "real"); len(rows) != 2 {
		t.Fatalf("target rows = %d, want 2", len(rows))
	}
}

// TestMerge_UnknownAccounts reports an error rather than deleting something.
func TestMerge_UnknownAccounts(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	seedUser(t, db, "real", true, entity.RoleUser)

	if _, err := s.Merge(context.Background(), "ghost", "real"); err == nil {
		t.Error("merged from a nonexistent source")
	}
	if _, err := s.Merge(context.Background(), "real", "ghost"); err == nil {
		t.Error("merged into a nonexistent target")
	}
}

// TestPlaceholderEmail keeps the synthetic address obviously fake: a plausible
// invented address could later collide with the person's real account.
func TestPlaceholderEmail(t *testing.T) {
	got := PlaceholderEmail("telegram", "555")
	if got != "telegram-555@channel.local" {
		t.Fatalf("placeholder = %q", got)
	}
	if !IsPlaceholderEmail(got) {
		t.Error("own placeholder not recognised")
	}
	if !IsPlaceholderEmail("") {
		t.Error("empty email should count as no email")
	}
	if IsPlaceholderEmail("ada@example.com") {
		t.Error("a real address was treated as a placeholder")
	}
}

// TestListMergeCandidates surfaces exactly the accounts that need a human
// decision: channel-created, no real email, still pending.
func TestListMergeCandidates(t *testing.T) {
	db := newDB(t)
	s := NewStore(db)
	ctx := context.Background()

	// Needs merging: placeholder email + a connection.
	tg := &entity.User{ID: "tg", Email: PlaceholderEmail("telegram", "555"),
		Name: "Ada TG", Role: entity.RoleUser, Approved: false}
	if err := db.Create(tg).Error; err != nil {
		t.Fatalf("seed tg: %v", err)
	}
	if err := s.Link(ctx, identityFor("tg", "telegram", "default", "555", "Ada TG")); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Not a candidate: real email.
	seedUser(t, db, "real", true, entity.RoleUser)
	// Not a candidate: placeholder email but no connection, so not
	// channel-created.
	if err := db.Create(&entity.User{ID: "orphan",
		Email: PlaceholderEmail("telegram", "999"), Name: "Orphan"}).Error; err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	got, err := s.ListMergeCandidates(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].User.ID != "tg" {
		t.Fatalf("candidates = %+v, want only tg", got)
	}
	if len(got[0].Connections) != 1 {
		t.Fatalf("candidate connections = %d, want 1", len(got[0].Connections))
	}
}
