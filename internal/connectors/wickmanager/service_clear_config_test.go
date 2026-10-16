package wickmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func newClearConfigHandlers(t *testing.T) (*handlers, *connectors.Service, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cfgs := configs.NewService(db)
	if err := cfgs.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	mod := connector.Module{
		Meta: connector.Meta{Key: "stub", Name: "Stub", Description: "test", Fixed: true},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
			{Key: "user_token", Type: "text", IsSecret: true},
			{Key: "pinned", Type: "text", Locked: true, Value: "keep"},
		},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("noop", "Noop", "noop", emptyInput{},
					func(*connector.Ctx) (any, error) { return nil, nil }, wickdocs.Docs{}),
			),
		},
	}
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgs)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	id := rows[0].ID
	if err := svc.Update(context.Background(), id, "Stub", map[string]string{"token": "t1", "user_token": "u1"}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return newHandlers(Deps{Configs: cfgs, Connectors: svc}), svc, id
}

func clearCtx(id, key string) *connector.Ctx {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	ctx := login.WithUser(context.Background(), admin, nil)
	return connector.NewCtx(ctx, "", nil, map[string]string{"id": id, "config_key": key}, nil, nil, nil)
}

func TestConnectorClearConfig(t *testing.T) {
	h, svc, id := newClearConfigHandlers(t)

	out, err := h.connectorClearConfig(clearCtx(id, "user_token"))
	if err != nil {
		t.Fatalf("clear optional secret: %v", err)
	}
	res := out.(map[string]any)
	if res["before"].(map[string]any)["is_set"] != true || res["after"].(map[string]any)["is_set"] != false {
		t.Errorf("before/after diff wrong: %v", res)
	}

	if _, err := h.connectorClearConfig(clearCtx(id, "token")); !errors.Is(err, errRequiredClear) {
		t.Errorf("required field err = %v, want errRequiredClear", err)
	}
	if _, err := h.connectorClearConfig(clearCtx(id, "pinned")); !errors.Is(err, errLockedRow) {
		t.Errorf("locked field err = %v, want errLockedRow", err)
	}
	if _, err := h.connectorClearConfig(clearCtx(id, "ghost")); err == nil {
		t.Error("unknown key must be refused")
	}

	cfg := svc.LoadConfigs(entity.Connector{ID: id, Key: "stub"})
	if cfg["user_token"] != "" || cfg["token"] != "t1" || cfg["pinned"] != "keep" {
		t.Fatalf("configs after clear = %v", cfg)
	}
}

func TestConnectorClearConfigNeedsUser(t *testing.T) {
	h, _, id := newClearConfigHandlers(t)
	c := connector.NewCtx(context.Background(), "", nil, map[string]string{"id": id, "config_key": "user_token"}, nil, nil, nil)
	if _, err := h.connectorClearConfig(c); !errors.Is(err, errNotAuthenticated) {
		t.Fatalf("err = %v, want errNotAuthenticated", err)
	}
}
