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
