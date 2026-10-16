package connectors

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

func TestClearConfig(t *testing.T) {
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	mod := echoModule()
	mod.Configs = append(mod.Configs, entity.Config{Key: "user_token", Type: "text", IsSecret: true})
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	id := rows[0].ID
	ctx := context.Background()
	if err := svc.Update(ctx, id, "Stub", map[string]string{"token": "t1", "user_token": "u1"}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.ClearConfig(ctx, id, "user_token"); err != nil {
		t.Fatalf("clear optional secret: %v", err)
	}
	if err := svc.ClearConfig(ctx, id, "token"); err == nil {
		t.Fatal("required field must be refused")
	}
	if err := svc.ClearConfig(ctx, id, "ghost"); err == nil {
		t.Fatal("undeclared key must be refused")
	}
	if err := svc.ClearConfig(ctx, "no-such-id", "user_token"); err == nil {
		t.Fatal("unknown connector must be refused")
	}
	cfg := svc.LoadConfigs(entity.Connector{ID: id, Key: "stub"})
	if cfg["user_token"] != "" || cfg["token"] != "t1" {
		t.Fatalf("configs after clear = %v, want user_token empty and token kept", cfg)
	}
}
