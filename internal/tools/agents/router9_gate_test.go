package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newGateTestConfigs(t *testing.T) *configs.Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	postgres.Migrate(db)
	svc := configs.NewService(db)
	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Register the agents owner key the gate reads/writes so SetOwned
	// accepts it (SetOwned rejects unregistered keys).
	if err := svc.EnsureOwned(context.Background(), "agents",
		entity.Config{Key: router9EnabledKey, Value: "true"},
	); err != nil {
		t.Fatalf("ensure owned: %v", err)
	}
	return svc
}

// TestRouter9EnabledDefault: absent config row = ON (default true).
func TestRouter9EnabledDefault(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	globalConfigs = newGateTestConfigs(t)

	if !Router9Enabled() {
		t.Error("absent row should default enabled=true")
	}
}

// TestRouter9EnabledToggle: setting the flag false flips Router9Enabled and
// makes the API proxy wrapper 404 (master kill).
func TestRouter9EnabledToggle(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	svc := newGateTestConfigs(t)
	globalConfigs = svc

	if err := svc.SetOwned(context.Background(), "agents", router9EnabledKey, "false"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if Router9Enabled() {
		t.Error("enabled=false not honored")
	}

	// API proxy wrapper must 404 when master is off (before touching backend).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/9router/v1/models", nil)
	Router9APIProxy().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("api proxy: got %d want 404 when disabled", rec.Code)
	}

	// Dashboard wrapper must also 404 when master is off.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/9router/", nil)
	Router9RootProxy().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("dashboard: got %d want 404 when disabled", rec2.Code)
	}
}
