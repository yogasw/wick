package agents

// 9router glue that needs the agents package's globals. The feature
// itself lives in the self-contained internal/tools/agents/9router
// package; this file only backs its ConfigStore with the app config
// service and exposes a boot hook for auto-start.

import (
	"context"
	"net/http"

	router9 "github.com/yogasw/wick/internal/tools/agents/9router"
)

// router9 config is stored on the agents owner so it sits alongside the
// other agents knobs. Key must match the snake-cased field name of
// GeneralConfig.Router9Autostart (the digit blocks an underscore, so it
// is "router9autostart", not "router9_autostart").
const router9AutostartKey = "router9autostart"

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

// Router9RootProxy is the dashboard reverse-proxy handler. server.go
// mounts it at the wick root under router9.MountPrefix behind admin auth.
func Router9RootProxy() http.Handler {
	return router9.RootProxy()
}

// Router9MountPrefix is the wick-root path the dashboard proxy is mounted
// under. Re-exported so server.go can wire the route without importing
// the 9router subpackage directly.
func Router9MountPrefix() string {
	return router9.MountPrefix
}
