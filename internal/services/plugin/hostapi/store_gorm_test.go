package hostapi

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
)

func newGormStore(t *testing.T) *GormStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&entity.PluginHostPermission{}, &entity.PluginHostCall{}); err != nil {
		t.Fatal(err)
	}
	return NewGormStore(db)
}

func TestGormStorePermissions(t *testing.T) {
	ctx := context.Background()
	s := newGormStore(t)
	p := NewPermissions(s)
	if err := p.Set(ctx, "p", "users.read", "", false, "admin"); err != nil {
		t.Fatal(err)
	}
	if d := p.Decide(ctx, PluginInfo{Key: "p"}, "users.read", ""); d.Allowed {
		t.Fatal("a stored false was read back as allowed")
	}
	if err := p.Set(ctx, "p", "users.read", "", true, "admin2"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.List(ctx, "p")
	if len(rows) != 1 || !rows[0].Allowed || rows[0].UpdatedBy != "admin2" {
		t.Fatalf("upsert rows = %+v", rows)
	}
	if err := p.Clear(ctx, "p", "users.read", ""); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.List(ctx, "p"); len(rows) != 0 {
		t.Fatalf("rows after clear = %+v", rows)
	}
}

func TestGormStoreAudit(t *testing.T) {
	ctx := context.Background()
	s := newGormStore(t)
	now := time.Now().UTC()
	err := s.WriteCalls(ctx, []entity.PluginHostCall{
		{Ts: now, PluginKey: "p", RPC: "GetTicket", OK: true, Code: "OK"},
		{Ts: now.Add(-8 * 24 * time.Hour), PluginKey: "p", RPC: "ListUsers", OK: true, Code: "OK"},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeCallsBefore(ctx, now.Add(-AuditRetention))
	if err != nil || n != 1 {
		t.Fatalf("purged %d, err %v; want 1", n, err)
	}
}
