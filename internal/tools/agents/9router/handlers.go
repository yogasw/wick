package router9

// HTTP wiring for the 9router feature. The page chrome (Agents shell)
// is supplied by the hosting package via SidebarFunc; everything else —
// the process manager singleton, admin gating, control + proxy routes,
// the settings page, and the auto-start boot hook — lives here so the
// feature is self-contained.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// mgr is the process-wide manager, created once with the proxy prefix
// bound to the hosting tool's base.
var (
	mgr  *Manager
	once sync.Once
)

// manager returns the singleton, lazily constructing it. The dashboard
// proxy mounts at the wick root (MountPrefix), not under the tool, so
// 9router's root-absolute URLs rewrite to a single prefix.
func manager() *Manager {
	once.Do(func() {
		mgr = New()
	})
	return mgr
}

// RootProxy returns the dashboard reverse-proxy handler for mounting at
// the wick root under MountPrefix+"/". server.go wires it behind admin
// auth. Exported because the iframe content is served from the root,
// not from under the tool base.
func RootProxy() http.Handler { return manager().ProxyHandler() }

// SidebarFunc builds the Agents shell layout VM for a 9router page,
// marking the given nav item active. The agents package owns the shell,
// so it passes this in rather than this package reaching back into it.
type SidebarFunc func(c *tool.Ctx, activePage string) view.AgentsLayoutVM

// ConfigStore persists the small set of 9router knobs (just auto-start
// today). The hosting package backs it with the app's config service so
// this package stays free of storage imports.
type ConfigStore interface {
	GetAutostart() bool
	SetAutostart(ctx context.Context, on bool) error
}

var store ConfigStore

// Register wires every 9router route onto r, relative to the tool base:
// the dashboard page, the settings page, control + autostart endpoints,
// the logs endpoint, and the dashboard proxy. cs persists settings.
func Register(r tool.Router, sidebar SidebarFunc, cs ConfigStore) {
	store = cs
	r.GET("/9router", func(c *tool.Ctx) { page(c, sidebar) })
	r.GET("/9router/status", status)
	r.GET("/9router/logs", logs)
	r.POST("/9router/install", install)
	r.POST("/9router/start", start)
	r.POST("/9router/stop", stop)
	r.POST("/9router/restart", restart)
	r.POST("/9router/autostart", setAutostart)
	// Note: the dashboard iframe is NOT served from under the tool. It is
	// proxied at the wick root (MountPrefix) and wired in server.go, so
	// 9router's root-absolute URLs rewrite to one prefix. See RootProxy.
}

// Autostart starts 9router at boot when the stored flag is on and the
// package is installed. Safe to call in a goroutine.
func Autostart() {
	m := manager()
	if store == nil {
		m.log.Warn().Msg("9router: autostart skipped — config store not wired")
		return
	}
	on := store.GetAutostart()
	m.log.Info().Bool("autostart", on).Msg("9router: autostart check")
	if !on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if !m.Installed(ctx) {
		m.log.Warn().Msg("9router: autostart on but package not installed — skipping")
		return
	}
	m.log.Info().Msg("9router: autostart starting process")
	if err := m.StartAndWait(ctx); err != nil {
		m.log.Error().Err(err).Msg("9router: autostart failed")
		return
	}
	m.log.Info().Msg("9router: autostart succeeded")
}

// ── pages ────────────────────────────────────────────────────────────

// page renders the single 9router page: a Dashboard tab (iframe) and a
// Settings tab (install/start/stop/restart + auto-start + logs), toggled
// client-side. FullBleed so the iframe fills the content area. Not
// admin-gated here — the nav link is already admin-only; control
// endpoints are gated individually.
func page(c *tool.Ctx, sidebar SidebarFunc) {
	layout := sidebar(c, "9router")
	layout.FullBleed = true
	autostart := false
	if store != nil {
		autostart = store.GetAutostart()
	}
	c.HTML(view.Router9Page(view.Router9VM{
		Layout:    layout,
		Base:      c.Base(),
		Autostart: autostart,
	}))
}

// ── control endpoints (admin only) ───────────────────────────────────

func adminOnly(c *tool.Ctx) bool {
	u := login.GetUser(c.Context())
	if u == nil || !u.IsAdmin() {
		c.Error(http.StatusForbidden, "admins only")
		return false
	}
	return true
}

func status(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Status(c.W, c.R)
}

func logs(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Logs(c.W, c.R)
}

func install(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Install(c.W, c.R)
}

func start(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Start(c.W, c.R)
}

func stop(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Stop(c.W, c.R)
}

func restart(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	manager().Restart(c.W, c.R)
}

// setAutostart persists the auto-start flag from form value "on"
// ("true"/"false").
func setAutostart(c *tool.Ctx) {
	if !adminOnly(c) {
		return
	}
	if store == nil {
		c.Error(http.StatusServiceUnavailable, "config store unavailable")
		return
	}
	on := c.Form("on") == "true"
	if err := store.SetAutostart(c.Context(), on); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"autostart": on})
}
