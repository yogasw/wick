package custom

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestUpdateDefPersistsAllowSessionConfig guards the regression where
// Store.UpdateDef's explicit column map omitted allow_session_config, so
// flipping the per-session capability on an existing definition never hit
// the DB (it stuck at the gorm default on reload).
func TestUpdateDefPersistsAllowSessionConfig(t *testing.T) {
	store := NewStore(newTestDB(t))
	ctx := context.Background()

	def := &entity.CustomConnector{
		Key:                "k1",
		Name:               "K1",
		Source:             entity.CustomConnectorSourceManual,
		Configs:            "[]",
		Ops:                "[]",
		AllowSessionConfig: true,
	}
	if err := store.CreateDef(ctx, def); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, _ := store.GetDef(ctx, def.ID); !got.AllowSessionConfig {
		t.Fatal("create did not persist allow_session_config=true")
	}

	// Flip OFF via UpdateDef — the path that previously dropped the column.
	def.AllowSessionConfig = false
	if err := store.UpdateDef(ctx, def); err != nil {
		t.Fatalf("update off: %v", err)
	}
	if got, _ := store.GetDef(ctx, def.ID); got.AllowSessionConfig {
		t.Fatal("update did not persist allow_session_config=false (regression)")
	}

	// Flip ON again.
	def.AllowSessionConfig = true
	if err := store.UpdateDef(ctx, def); err != nil {
		t.Fatalf("update on: %v", err)
	}
	if got, _ := store.GetDef(ctx, def.ID); !got.AllowSessionConfig {
		t.Fatal("update did not persist allow_session_config=true")
	}
}
