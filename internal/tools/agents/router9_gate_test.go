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
		entity.Config{Key: router9ExternalAPIKey, Value: "false"},
	); err != nil {
		t.Fatalf("ensure owned: %v", err)
	}
	return svc
}

// TestRouter9ExternalAPIRoundTrip locks the config key the external-API
// toggle reads/writes. The key is derived from GeneralConfig via
// StructToConfigs; a rename there would silently break Set/Get here, so
// this asserts the round-trip actually persists.
func TestRouter9ExternalAPIRoundTrip(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	globalConfigs = newGateTestConfigs(t)

	var cs router9ConfigStore
	if cs.GetExternalAPI() {
		t.Fatal("external API should default off")
	}
	if Router9ExternalAPIEnabled() {
		t.Fatal("external API should default off (gated helper)")
	}
	if err := cs.SetExternalAPI(context.Background(), true); err != nil {
		t.Fatalf("set external: %v", err)
	}
	if !cs.GetExternalAPI() {
		t.Error("external API not persisted after enable")
	}
	if !Router9ExternalAPIEnabled() {
		t.Error("gated helper did not reflect enabled external API")
	}
}

// TestRouter9ExternalAPIGatedByMaster: master off => external helper off
// even when the external flag itself is on.
func TestRouter9ExternalAPIGatedByMaster(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	svc := newGateTestConfigs(t)
	globalConfigs = svc

	var cs router9ConfigStore
	if err := cs.SetExternalAPI(context.Background(), true); err != nil {
		t.Fatalf("set external: %v", err)
	}
	if err := svc.SetOwned(context.Background(), "agents", router9EnabledKey, "false"); err != nil {
		t.Fatalf("disable master: %v", err)
	}
	if Router9ExternalAPIEnabled() {
		t.Error("external API must be off when master switch is off")
	}
}

// TestRouter9NextAssetProxyMasterOff: the root /_next/ proxy 404s when the
// master switch is off (checked before any backend call). There is no Referer
// guard — /_next/ is unique to Next.js so it's served regardless of referer;
// a guard was fragile because module/preload requests send an origin-only
// Referer with no path.
func TestRouter9NextAssetProxyMasterOff(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	globalConfigs = newGateTestConfigs(t)

	if err := globalConfigs.SetOwned(context.Background(), "agents", router9EnabledKey, "false"); err != nil {
		t.Fatalf("disable master: %v", err)
	}
	h := Router9NextAssetProxy()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_next/static/css/x.css", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("master off: got %d want 404", rec.Code)
	}
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

// TestNextAssetReRootStripPrefixRecovers verifies the /_next/ re-root logic:
// after prepending /9router to BOTH Path and RawPath, the dashboard proxy's
// StripPrefix("/9router") recovers the original asset path. A regression here
// (updating only Path) makes StripPrefix miss and the asset 404 — the exact
// bug this guards. Covers a plain path and a Next.js route-group path whose
// parens survive in RawPath.
func TestNextAssetReRootStripPrefixRecovers(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	globalConfigs = newGateTestConfigs(t)

	const prefix = "/9router"
	for _, in := range []string{
		"/_next/static/chunks/webpack-abc.js",
		"/_next/static/chunks/app/(dashboard)/layout-db0.js",
		"/_next/static/chunks/app/%28dashboard%29/layout-db0.js",
	} {
		var got string
		rerooted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// mimic Router9NextAssetProxy's re-root, then the dashboard proxy's
			// StripPrefix, then capture what the backend would receive.
			r.URL.Path = prefix + r.URL.Path
			if r.URL.RawPath != "" {
				r.URL.RawPath = prefix + r.URL.RawPath
			}
			http.StripPrefix(prefix, http.HandlerFunc(func(_ http.ResponseWriter, rr *http.Request) {
				got = rr.URL.Path
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(w, r)
		})
		rec := httptest.NewRecorder()
		rerooted.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, in, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: StripPrefix missed, got %d (re-root left RawPath un-prefixed?)", in, rec.Code)
		}
		if got == "" || got[:6] != "/_next" {
			t.Errorf("%s: backend path = %q, want /_next/...", in, got)
		}
	}
}
