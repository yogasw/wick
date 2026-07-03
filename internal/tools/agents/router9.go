package agents

// 9router glue that needs the agents package's globals. The feature
// itself lives in the self-contained internal/tools/agents/9router
// package; this file only backs its ConfigStore with the app config
// service and exposes a boot hook for auto-start.

import (
	"context"
	"net/http"

	"github.com/yogasw/wick/internal/login"
	router9 "github.com/yogasw/wick/internal/tools/agents/9router"
)

// router9 config is stored on the agents owner so it sits alongside the
// other agents knobs. Keys must match the snake-cased field names of the
// GeneralConfig fields — the digit blocks an underscore, so it is
// "router9autostart" / "router9enabled", not "router9_autostart".
const (
	router9AutostartKey = "router9autostart"
	router9EnabledKey   = "router9enabled"
	// Derived key from GeneralConfig.Router9ExternalAPI via StructToConfigs:
	// the digit blocks an underscore after "router9" but the External→API
	// boundary keeps one, so it is "router9external_api".
	router9ExternalAPIKey = "router9external_api"
)

// router9ConfigStore implements router9.ConfigStore over globalConfigs.
type router9ConfigStore struct{}

func (router9ConfigStore) GetAutostart() bool {
	if globalConfigs == nil {
		return false
	}
	return globalConfigs.GetOwned("agents", router9AutostartKey) == "true"
}

func (router9ConfigStore) SetAutostart(ctx context.Context, on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	return globalConfigs.SetOwned(ctx, "agents", router9AutostartKey, val)
}

func (router9ConfigStore) GetExternalAPI() bool {
	if globalConfigs == nil {
		return false
	}
	return globalConfigs.GetOwned("agents", router9ExternalAPIKey) == "true"
}

func (router9ConfigStore) SetExternalAPI(ctx context.Context, on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	return globalConfigs.SetOwned(ctx, "agents", router9ExternalAPIKey, val)
}

// ExternalAPIAllowed reports whether off-machine callers may reach the
// /9router/v1 API. Gated by the master switch too — a disabled 9router
// exposes nothing regardless of this flag.
func (s router9ConfigStore) ExternalAPIAllowed() bool {
	return Router9Enabled() && s.GetExternalAPI()
}

// Enabled backs the master switch for the control endpoints.
func (router9ConfigStore) Enabled() bool { return Router9Enabled() }

// AccessAllowed reports whether the request's user may drive 9router
// controls. Admin-only.
func (router9ConfigStore) AccessAllowed(ctx context.Context) bool {
	return router9AdminOnly(ctx)
}

// Router9AutostartEnabled reports whether the admin turned on 9router
// auto-start. server.go calls this to decide whether to add a boot-gate
// step (so the "Booting…" screen waits for 9router) — skipping it
// entirely when off keeps boot instant.
func Router9AutostartEnabled() bool {
	return router9ConfigStore{}.GetAutostart()
}

// Router9Autostart starts 9router at boot when the stored auto-start
// flag is on. Called from server.go after the tool router mounts (so the
// config store is wired); safe in a goroutine.
func Router9Autostart() {
	router9.Autostart()
}

// Router9Enabled reports the master switch. Absent config row = ON
// (default true, matching DefaultGeneralConfig) so an existing setup keeps
// working until an admin explicitly disables it. When off, every 9router
// surface (dashboard, /v1 proxy, autostart, controls) is dead.
func Router9Enabled() bool {
	if globalConfigs == nil {
		return true
	}
	v := globalConfigs.GetOwned("agents", router9EnabledKey)
	return v == "" || v == "true"
}

// Router9ExternalAPIEnabled reports whether the admin exposed the
// /9router/v1 API to off-machine callers. server.go reads this to decide
// whether a non-loopback request to the API subtree bypasses the host
// allowlist (so a tunnel/public host reaches 9router, which then enforces
// its own API key). Gated by the master switch.
func Router9ExternalAPIEnabled() bool {
	return router9ConfigStore{}.ExternalAPIAllowed()
}

// router9AdminOnly reports whether the request's user is an admin. The
// 9router dashboard + controls are admin-only. Master-off is handled
// separately by Router9Enabled. Fail-closed when no user.
func router9AdminOnly(ctx context.Context) bool {
	u := login.GetUser(ctx)
	return u != nil && u.IsAdmin()
}

// Router9RootProxy is the dashboard reverse-proxy handler, wrapped so it
// 404s when the master switch is off. Admin auth is applied at the mount
// (RequireAdmin) in server.go. server.go mounts it at the wick root.
func Router9RootProxy() http.Handler {
	inner := router9.RootProxy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Router9Enabled() {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// Router9APIProxy is the OpenAI-compatible API reverse-proxy handler.
// server.go mounts it at router9.MountPrefix+"/v1/" WITHOUT auth so local
// AI CLIs pointed at wick's /9router/v1 base URL can reach 9router. Still
// gated by the master switch (404 when off).
func Router9APIProxy() http.Handler {
	inner := router9.APIProxy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Router9Enabled() {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// Router9MountPrefix is the wick-root path the dashboard proxy is mounted
// under. Re-exported so server.go can wire the route without importing
// the 9router subpackage directly.
func Router9MountPrefix() string {
	return router9.MountPrefix
}

// Router9NextAssetProxy handles root-absolute /_next/* asset requests that
// 9router's Next.js bundle emits at runtime and which slip past the body
// rewriter (fonts/CSS/chunks assembled from JS fragments, so the string
// rewrite in rewrite.go can't catch them). The iframe is same-origin, so
// those requests land at the wick root as /_next/... with no prefix and 404.
//
// Rather than chase every runtime-assembled path in the rewriter, we catch
// them here: re-root the path under MountPrefix and hand it to the dashboard
// proxy (which strips the prefix back off before forwarding). /_next/ is
// unique to Next.js so serving it at the wick root can't collide with any
// wick route — no Referer guard needed (and a guard was in fact fragile:
// module-script / preload requests send an origin-only Referer with no path
// under strict-origin-when-cross-origin, so a /9router-path check dropped
// legitimate assets and 404'd them). Admin auth is applied at the mount in
// server.go, like the dashboard proxy.
func Router9NextAssetProxy() http.Handler {
	inner := router9.RootProxy()
	prefix := router9.MountPrefix
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Router9Enabled() {
			http.NotFound(w, r)
			return
		}
		// Re-root under the mount prefix so the dashboard proxy's StripPrefix
		// yields the original /_next/... path for the backend. Both Path and
		// RawPath must be updated: StripPrefix compares EscapedPath(), which is
		// derived from RawPath when set — updating only Path leaves RawPath at
		// the un-prefixed value, StripPrefix fails to match /9router, and the
		// asset 404s. Next.js route groups like /(dashboard)/ keep parens in
		// RawPath, so we can't just clear it. prefix has no chars needing
		// escaping, so prepending it to both is safe.
		r.URL.Path = prefix + r.URL.Path
		if r.URL.RawPath != "" {
			r.URL.RawPath = prefix + r.URL.RawPath
		}
		inner.ServeHTTP(w, r)
	})
}
