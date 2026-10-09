package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

var errBadToken = errors.New("token is not valid")

// newSvcWithValidator boots a stub connector whose token must start with
// "ok-", with a second plain field to edit around it.
func newSvcWithValidator(t *testing.T) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	mod := echoModule()
	mod.Configs = append(mod.Configs, entity.Config{Key: "note", Type: "text"})
	mod.ValidateConfig = func(key, value string) error {
		if key == "token" && !strings.HasPrefix(value, "ok-") {
			return errBadToken
		}
		return nil
	}
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	return svc, rows[0].ID
}

func TestUpdateRejectsInvalidConfig(t *testing.T) {
	svc, id := newSvcWithValidator(t)
	ctx := context.Background()
	if err := svc.Update(ctx, id, "Stub", map[string]string{"token": "ok-1"}, false); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	err := svc.Update(ctx, id, "Stub", map[string]string{"token": "bad", "note": "n1"}, false)
	if !errors.Is(err, errBadToken) {
		t.Fatalf("invalid token err = %v, want errBadToken", err)
	}
	// Refused as a whole: neither field was written.
	cfg := svc.LoadConfigs(entity.Connector{ID: id, Key: "stub"})
	if cfg["token"] != "ok-1" || cfg["note"] != "" {
		t.Fatalf("configs after refused save = %v, want token ok-1 and no note", cfg)
	}
	// A blank secret keeps the stored value and is not validated.
	if err := svc.Update(ctx, id, "Stub", map[string]string{"token": ""}, false); err != nil {
		t.Fatalf("blank secret: %v", err)
	}
}

func TestUpdateSkipsUnchangedInvalidValue(t *testing.T) {
	svc, id := newSvcWithValidator(t)
	ctx := context.Background()
	// A bad value saved before validation existed.
	if err := svc.cfgs.SetOwned(ctx, ownerForConnector(id), "token", "legacy"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	stored := svc.LoadConfigs(entity.Connector{ID: id, Key: "stub"})
	stored["note"] = "n2"
	if err := svc.Update(ctx, id, "Stub", stored, false); err != nil {
		t.Fatalf("editing another field must not trip on the stored token: %v", err)
	}
}

func TestCreateRejectsInvalidConfig(t *testing.T) {
	svc, _ := newSvcWithValidator(t)
	mod, _ := svc.Module("stub")
	mod.Meta.Fixed = false
	mod.Meta.Key = "stub2"
	if err := svc.UpsertModule(context.Background(), mod); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_, err := svc.Create(context.Background(), "stub2", "Stub 2", map[string]string{"token": "bad"}, "")
	if !errors.Is(err, errBadToken) {
		t.Fatalf("create err = %v, want errBadToken", err)
	}
}
