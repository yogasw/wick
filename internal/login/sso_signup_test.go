package login

import (
	"context"
	"errors"
	"testing"
)

func TestUpsertSSOUserPolicy(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newLoginSQLite(t), "boss@acme.test")
	// Seed an owner so the first-user rule does not touch the cases below.
	if _, err := svc.UpsertUser(ctx, "boss@acme.test", "Boss", ""); err != nil {
		t.Fatal(err)
	}

	u, err := svc.UpsertSSOUser(ctx, "Dev@Acme.test", "Dev", "", true, true)
	if err != nil || !u.Approved || u.IsAdmin() {
		t.Fatalf("auto-approve new: %+v, %v (want approved, not admin)", u, err)
	}

	u, err = svc.UpsertSSOUser(ctx, "guest@other.test", "Guest", "", true, false)
	if err != nil || u.Approved {
		t.Fatalf("pending new: %+v, %v (want unapproved)", u, err)
	}
	// Same pending account later signs in after its domain is auto-approved.
	u, err = svc.UpsertSSOUser(ctx, "guest@other.test", "Guest", "", false, true)
	if err != nil || !u.Approved {
		t.Fatalf("approve existing: %+v, %v", u, err)
	}

	if _, err := svc.UpsertSSOUser(ctx, "new@other.test", "New", "", false, false); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration: err = %v, want ErrRegistrationClosed", err)
	}
	if _, err := svc.GetUserByEmail(ctx, "new@other.test"); err == nil {
		t.Fatal("closed registration still created an account")
	}
	// Admin emails always get in, even with registration closed.
	if u, err := svc.UpsertSSOUser(ctx, "boss@acme.test", "Boss", "", false, false); err != nil || !u.Approved {
		t.Fatalf("admin with closed registration: %+v, %v", u, err)
	}
}
