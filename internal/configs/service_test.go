package configs

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newTestSvc(t *testing.T) *Service {
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
	return NewService(db)
}

func TestEnsureOwnedSeedsRowsAndStampsOwner(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()

	rows := []entity.Config{
		{Key: "url", Type: "url", Value: "http://abc.com", Required: true},
		{Key: "token", Type: "text", IsSecret: true, Required: true},
	}
	if err := svc.EnsureOwned(ctx, "connector:abc", rows...); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	got := svc.ListOwned("connector:abc")
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].Key != "url" || got[1].Key != "token" {
		t.Fatalf("declaration order broken: %+v", got)
	}
	if got[0].Owner != "connector:abc" {
		t.Fatalf("owner not stamped: %q", got[0].Owner)
	}
	if got[0].Value != "http://abc.com" {
		t.Fatalf("seeded value missing: %q", got[0].Value)
	}

	missing := svc.Missing("connector:abc")
	if len(missing) != 1 || missing[0] != "token" {
		t.Fatalf("missing should be [token], got %v", missing)
	}
}

func TestEnsureOwnedRefreshesMetaPreservesValue(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()

	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "text", Description: "v1"},
	); err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	if err := svc.SetOwned(ctx, "connector:abc", "token", "real-value-123"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "token", Type: "text", Description: "v2-renamed", IsSecret: true},
	); err != nil {
		t.Fatalf("ensure 2: %v", err)
	}

	got := svc.ListOwned("connector:abc")
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].Value != "real-value-123" {
		t.Fatalf("value clobbered on re-ensure: %q", got[0].Value)
	}
	if got[0].Description != "v2-renamed" || !got[0].IsSecret {
		t.Fatalf("meta not refreshed: %+v", got[0])
	}
}

func TestDeleteOwnedRemovesRowsAndCache(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()

	if err := svc.EnsureOwned(ctx, "connector:abc",
		entity.Config{Key: "url", Value: "http://abc.com"},
		entity.Config{Key: "token", Value: "t"},
	); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := svc.EnsureOwned(ctx, "connector:def",
		entity.Config{Key: "url", Value: "http://def.com"},
	); err != nil {
		t.Fatalf("ensure other: %v", err)
	}

	if err := svc.DeleteOwned(ctx, "connector:abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := svc.ListOwned("connector:abc"); len(got) != 0 {
		t.Fatalf("rows survived in cache: %+v", got)
	}
	rows, err := svc.repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if r.Owner == "connector:abc" {
			t.Fatalf("row survived in DB: %+v", r)
		}
	}
	if got := svc.ListOwned("connector:def"); len(got) != 1 {
		t.Fatalf("unrelated owner clobbered: %+v", got)
	}
}

func TestDeleteOwnedNoopWhenEmpty(t *testing.T) {
	svc := newTestSvc(t)
	if err := svc.DeleteOwned(context.Background(), "connector:nope"); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
}
