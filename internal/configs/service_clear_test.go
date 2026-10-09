package configs

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestClearOwnedEmptiesSecret(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	const owner = "connector:abc"
	if err := svc.EnsureOwned(ctx, owner,
		entity.Config{Key: "token", Type: "text", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	mustSet(t, svc, owner, "token", "s3cret")

	// The keep-on-empty shortcut still holds for SetOwned.
	mustSet(t, svc, owner, "token", "")
	if got := svc.GetOwned(owner, "token"); got != "s3cret" {
		t.Fatalf("SetOwned(\"\") must keep the secret, got %q", got)
	}

	if err := svc.ClearOwned(ctx, owner, "token"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := svc.GetOwned(owner, "token"); got != "" {
		t.Fatalf("cache not cleared: %q", got)
	}
	row, err := svc.repo.FindByOwnerKey(ctx, owner, "token")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Value != "" {
		t.Fatalf("db value not cleared: %q", row.Value)
	}
}

func TestClearOwnedRefusals(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	const owner = "connector:abc"
	if err := svc.EnsureOwned(ctx, owner,
		entity.Config{Key: "pinned", Type: "text", IsSecret: true, Locked: true, Value: "keep"},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := svc.ClearOwned(ctx, owner, "pinned"); err == nil {
		t.Fatal("locked row must be refused")
	}
	if got := svc.GetOwned(owner, "pinned"); got != "keep" {
		t.Fatalf("locked row value changed: %q", got)
	}
	if err := svc.ClearOwned(ctx, owner, "nope"); err == nil {
		t.Fatal("undeclared key must be refused")
	}

	if err := svc.EnsureOwned(ctx, "", entity.Config{Key: KeyDNSServers, Type: "text"}); err != nil {
		t.Fatalf("ensure app row: %v", err)
	}
	t.Setenv("WICK_DNS_SERVERS", "1.1.1.1")
	if err := svc.ClearOwned(ctx, "", KeyDNSServers); err == nil {
		t.Fatal("env-overridden row must be refused")
	}
}
