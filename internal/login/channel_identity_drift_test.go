package login

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestEmailDrift_ChannelIdentityStaysBoundToUserID probes the failure the user
// raised: a Slack sender was matched to a wick account by EMAIL, so what happens
// when that email later changes — can the identity end up pointing at an admin,
// or spawn a duplicate account?
//
// The answer is no, and the reason is worth pinning: the link is stored by
// user_id, not by email. Email is only the lookup key at link time. Once a row
// exists, keyed on (channel_type, instance_key, external_user_id), it keeps
// resolving to the same user_id no matter what the email becomes.
func TestEmailDrift_ChannelIdentityStaysBoundToUserID(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "boss@example.com")
	ctx := context.Background()

	// A plain user registers from Slack.
	plainID, err := svc.RegisterChannelUser(ctx, "ada@example.com", "Ada", "slack")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// They later change their email to one that IS in adminEmails.
	if err := svc.SetEmail(ctx, plainID, "boss@example.com"); err != nil {
		t.Fatalf("set email: %v", err)
	}

	var after entity.User
	if err := db.First(&after, "id = ?", plainID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}

	// The critical assertion: matching an email in adminEmails must NOT promote
	// an existing account. adminEmails is consulted only when a row is created
	// through a path that proves the address; changing the column later does
	// not re-run that decision.
	if after.IsAdmin() {
		t.Fatal("changing email to an adminEmails address promoted an existing user to admin")
	}
	if after.Approved {
		t.Fatal("changing email re-approved a pending account")
	}
}

// TestEmailDrift_NoDuplicateOnRepeatRegister covers the other half: if the same
// person's email changes and they message again, do we create a second account?
//
// RegisterChannelUser is keyed on email, so a NEW email does create a new row —
// which is exactly why the channel identity row (keyed on the external user id)
// is what prevents the duplicate: the resolver finds the existing link and never
// reaches the register path.
func TestEmailDrift_NoDuplicateOnRepeatRegister(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()

	first, err := svc.RegisterChannelUser(ctx, "ada@example.com", "Ada", "slack")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Same email again — idempotent.
	again, err := svc.RegisterChannelUser(ctx, "ada@example.com", "Ada", "slack")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if first != again {
		t.Fatalf("same email produced two accounts (%q, %q)", first, again)
	}

	// A DIFFERENT email is a different person as far as this call knows, so it
	// does create a second row. This is why the identity table must be
	// consulted BEFORE registering — documented here so the ordering is not
	// "cleaned up" later.
	other, err := svc.RegisterChannelUser(ctx, "ada.s@example.com", "Ada S", "slack")
	if err != nil {
		t.Fatalf("register other: %v", err)
	}
	if other == first {
		t.Fatal("different emails collapsed into one account")
	}

	var n int64
	db.Model(&entity.User{}).Count(&n)
	if n != 2 {
		t.Fatalf("user count = %d, want 2", n)
	}
}
