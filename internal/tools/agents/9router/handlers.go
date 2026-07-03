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

// APIProxy returns the OpenAI-compatible API reverse-proxy handler for
// mounting at MountPrefix+"/v1/". server.go wires it WITHOUT auth so
// local AI CLIs (codex/claude) pointed at wick's /9router/v1 base URL
// can reach 9router directly. The longer route pattern (/9router/v1/)
// takes precedence over the admin-gated dashboard mount (/9router/).
func APIProxy() http.Handler { return manager().APIProxyHandler() }

// SidebarFunc builds the Agents shell layout VM for a 9router page,
// marking the given nav item active. The agents package owns the shell,
// so it passes this in rather than this package reaching back into it.
type SidebarFunc func(c *tool.Ctx, activePage string) view.AgentsLayoutVM

// ConfigStore persists the small set of 9router knobs and answers the
// access questions the control endpoints need. The hosting package backs
// it with the app's config service + auth so this package stays free of
// storage/login imports.
type ConfigStore interface {
	GetAutostart() bool
	SetAutostart(ctx context.Context, on bool) error
	GetExternalAPI() bool
	SetExternalAPI(ctx context.Context, on bool) error
	// Enabled is the master switch — false disables every control endpoint.
	Enabled() bool
	// AccessAllowed reports whether the request's user may drive controls
	// (admin or a granted access tag).
	AccessAllowed(ctx context.Context) bool
	// ExternalAPIAllowed reports whether the /9router/v1 API may be reached
	// from off-machine (tunnel / public URL). Off = the proxy answers only
	// loopback callers and forwards them as local (no key); remote callers
	// are rejected. On = remote callers are forwarded with their real client
	// address so 9router enforces its own API key for non-local traffic.
	ExternalAPIAllowed() bool
}

var store ConfigStore

// assetURL resolves the hashed Vite bundle path for the 9router SPA. Wired
// by the agents package (which owns the embed) so this package doesn't
// import the SPA loader. nil until wired → empty string (shell shows a
// "bundle not built" hint).
var assetURL func() string

// Register wires every 9router route onto r, relative to the tool base:
// the SPA page, control + autostart/external endpoints, the logs + request
// stream endpoints, and the dashboard proxy. cs persists settings; asset
// resolves the SPA bundle URL.
func Register(r tool.Router, sidebar SidebarFunc, cs ConfigStore, asset func() string) {
	store = cs
	assetURL = asset
	// Back the API proxy's external-access decision with the config knob.
	manager().SetExternalAllowed(cs.ExternalAPIAllowed)
	r.GET("/9router", func(c *tool.Ctx) { page(c, sidebar) })
	r.GET("/9router/status", status)
	r.GET("/9router/logs", logs)
	r.GET("/9router/logstream", logstream)
	r.POST("/9router/install", install)
	r.POST("/9router/start", start)
	r.POST("/9router/stop", stop)
	r.POST("/9router/restart", restart)
	r.POST("/9router/autostart", setAutostart)
	r.POST("/9router/external", setExternal)
	r.GET("/9router/reqstream", reqstream)
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

// page renders the 9router SPA thin-shell inside the agents chrome. The
// SPA (fe/agents/router9) owns the Dashboard / Requests / Settings tabs;
// this handler only supplies the layout, base, seed toggles, and the Vite
// bundle URL. FullBleed so the iframe/stream fills the content area. Gated
// the same as the controls: 404 when the master switch is off, 403 for
// callers without access (admin or a granted tag).
func page(c *tool.Ctx, sidebar SidebarFunc) {
	if !allowed(c) {
		return
	}
	layout := sidebar(c, "9router")
	layout.FullBleed = true
	autostart, external := false, false
	if store != nil {
		autostart = store.GetAutostart()
		external = store.GetExternalAPI()
	}
	asset := ""
	if assetURL != nil {
		asset = assetURL()
	}
	c.HTML(view.Router9Page(view.Router9VM{
		Layout:      layout,
		Base:        c.Base(),
		AssetURL:    asset,
		Autostart:   autostart,
		ExternalAPI: external,
	}))
}

// ── control endpoints (admin only) ───────────────────────────────────

// allowed gates every control endpoint: the master switch must be on and
// the caller must be an admin (via the store). A disabled master returns
// 404 so the feature looks absent; a non-admin caller gets 403.
func allowed(c *tool.Ctx) bool {
	if store == nil || !store.Enabled() {
		c.Error(http.StatusNotFound, "9router disabled")
		return false
	}
	if !store.AccessAllowed(c.Context()) {
		c.Error(http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

func status(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Status(c.W, c.R)
}

func logs(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Logs(c.W, c.R)
}

// logstream is the SSE endpoint streaming live 9router process output to
// the Settings tab (snapshot on connect, then incremental tail). Admin-gated.
func logstream(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().LogStream(c.W, c.R)
}

func install(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Install(c.W, c.R)
}

func start(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Start(c.W, c.R)
}

func stop(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Stop(c.W, c.R)
}

func restart(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().Restart(c.W, c.R)
}

// setAutostart persists the auto-start flag from form value "on"
// ("true"/"false").
func setAutostart(c *tool.Ctx) {
	if !allowed(c) {
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

// setExternal persists the external-API flag from form value "on"
// ("true"/"false"). When on, the /9router/v1 proxy forwards remote
// callers to 9router with their real client address so 9router enforces
// its own API key; when off, remote callers are rejected and only local
// spawns reach the API.
func setExternal(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	if store == nil {
		c.Error(http.StatusServiceUnavailable, "config store unavailable")
		return
	}
	on := c.Form("on") == "true"
	if err := store.SetExternalAPI(c.Context(), on); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"external": on})
}

// reqstream is the SSE endpoint streaming live /9router/v1 API requests to
// the Requests tab. Nothing is stored: events are captured only while a
// client is connected and delivered in real time. Admin-gated like every
// control.
func reqstream(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	manager().ReqStream(c.W, c.R)
}
