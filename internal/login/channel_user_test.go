package login

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestRegisterChannelUser_CreatesUnapproved is the security core of
// channel auto-registration.
//
// A chat channel reports an email; it does not prove the sender controls it,
// and workspace membership is not the same claim as a wick registration. So
// the account must arrive inert: unapproved, and never admin.
func TestRegisterChannelUser_CreatesUnapproved(t *testing.T) {
	db := newLoginSQLite(t)
	// The email is in adminEmails on purpose: a Slack sender must NOT inherit
	// admin from it, because that path never proves control of the address.
	svc := NewService(db, "boss@example.com")

	id, err := svc.RegisterChannelUser(context.Background(), "boss@example.com", "Boss", "slack")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var u entity.User
	if err := db.First(&u, "id = ?", id).Error; err != nil {
		t.Fatalf("load created user: %v", err)
	}
	if u.Approved {
		t.Error("channel-registered user is approved — it could act before any admin reviewed it")
	}
	if u.IsAdmin() {
		t.Error("channel-registered user got admin from adminEmails; that requires proving the address")
	}
	if u.Role != entity.RoleUser {
		t.Errorf("role = %q, want %q", u.Role, entity.RoleUser)
	}
	if u.PasswordHash != "" {
		t.Error("channel-registered user has a password hash; it should not be directly signable")
	}
}

// TestRegisterChannelUser_IsIdempotent: a repeat message from the same sender
// must not pile up duplicate rows, and must not resurrect an approval.
func TestRegisterChannelUser_IsIdempotent(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()

	first, err := svc.RegisterChannelUser(ctx, "ada@example.com", "Ada", "slack")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	second, err := svc.RegisterChannelUser(ctx, "ada@example.com", "Ada", "slack")
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if first != second {
		t.Fatalf("ids differ (%q vs %q) — duplicate accounts for one email", first, second)
	}

	var n int64
	db.Model(&entity.User{}).Where("email = ?", "ada@example.com").Count(&n)
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}

// TestRegisterChannelUser_DoesNotDowngradeExisting: an already-approved user
// messaging from Slack must keep their approval and role.
func TestRegisterChannelUser_DoesNotDowngradeExisting(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")

	existing := entity.User{Email: "admin@example.com", Name: "Admin",
		Role: entity.RoleAdmin, Approved: true}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	id, err := svc.RegisterChannelUser(context.Background(), "admin@example.com", "Admin", "slack")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id != existing.ID {
		t.Fatalf("id = %q, want existing %q", id, existing.ID)
	}

	var u entity.User
	db.First(&u, "id = ?", existing.ID)
	if !u.Approved || !u.IsAdmin() {
		t.Fatalf("existing admin was downgraded: approved=%v admin=%v", u.Approved, u.IsAdmin())
	}
}

// TestRegisterChannelUser_RejectsBadEmail keeps a blank or malformed address
// from becoming an account.
func TestRegisterChannelUser_RejectsBadEmail(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	for _, email := range []string{"", "   ", "not-an-email"} {
		if _, err := svc.RegisterChannelUser(context.Background(), email, "X", "slack"); err == nil {
			t.Errorf("email %q was accepted", email)
		}
	}
}

// TestFindUserIDByEmail resolves case-insensitively so Slack casing does not
// miss an existing account (and then auto-create a duplicate).
func TestFindUserIDByEmail(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	u := entity.User{Email: "ada@example.com", Name: "Ada", Approved: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	if id, ok := svc.FindUserIDByEmail(context.Background(), "Ada@Example.COM"); !ok || id != u.ID {
		t.Fatalf("mixed-case lookup = (%q, %v), want (%q, true)", id, ok, u.ID)
	}
	if _, ok := svc.FindUserIDByEmail(context.Background(), "nobody@example.com"); ok {
		t.Error("unknown email reported as found")
	}
	if _, ok := svc.FindUserIDByEmail(context.Background(), ""); ok {
		t.Error("empty email reported as found")
	}
}
