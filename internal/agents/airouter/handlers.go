package airouter

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// store persists the airouter knobs and answers access questions. Wired once
// by Register; nil until then (every gate fails closed).
var store ConfigStore

// RegisterRoutes wires the generic control + stream endpoints for every
// registered router under /airouter/<id>/… (relative to the hosting tool base)
// and backs each Manager's external-API decision with the config store. The
// dashboard iframe + /v1 API proxies are mounted at the wick root by server.go
// via RootProxy / APIProxy — not here.
func RegisterRoutes(r tool.Router, cfg ConfigStore) {
	store = cfg
	// A registry-wide list so the FE switcher can enumerate routers.
	r.GET("/airouter/routers", listRouters)
	for _, rt := range List() {
		rt := rt
		id := rt.Desc.ID
		// Back the API proxy's external decision with the config knob.
		rt.Mgr.SetExternalAllowed(func() bool { return cfg.ExternalAPIAllowed(id) })

		r.GET("/airouter/"+id+"/status", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Status(c.W, c.R) }))
		r.GET("/airouter/"+id+"/logs", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Logs(c.W, c.R) }))
		r.GET("/airouter/"+id+"/logstream", ctl(rt, func(m *Manager, c *tool.Ctx) { m.LogStream(c.W, c.R) }))
		r.GET("/airouter/"+id+"/reqstream", ctl(rt, func(m *Manager, c *tool.Ctx) { m.ReqStream(c.W, c.R) }))
		r.POST("/airouter/"+id+"/install", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Install(c.W, c.R) }))
		r.POST("/airouter/"+id+"/start", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Start(c.W, c.R) }))
		r.POST("/airouter/"+id+"/stop", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Stop(c.W, c.R) }))
		r.POST("/airouter/"+id+"/restart", ctl(rt, func(m *Manager, c *tool.Ctx) { m.Restart(c.W, c.R) }))
		r.POST("/airouter/"+id+"/autostart", ctl(rt, setAutostart(id)))
		r.POST("/airouter/"+id+"/external", ctl(rt, setExternal(id)))
	}
}

// ctl wraps a per-router control handler with the shared gate: master switch
// on + caller has access.
func ctl(rt *Router, fn func(*Manager, *tool.Ctx)) tool.HandlerFunc {
	return func(c *tool.Ctx) {
		if !allowed(c) {
			return
		}
		fn(rt.Mgr, c)
	}
}

// allowed gates every control endpoint: master switch on + caller allowed.
// A disabled master returns 404 so the feature looks absent; a non-admin
// gets 403.
func allowed(c *tool.Ctx) bool {
	if store == nil || !store.Enabled() {
		c.Error(http.StatusNotFound, "airouter disabled")
		return false
	}
	if !store.AccessAllowed(c.Context()) {
		c.Error(http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// routerInfo is the switcher payload for one router.
type routerInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Blurb     string `json:"blurb"`
	Icon      string `json:"icon"`
	Autostart bool   `json:"autostart"`
	External  bool   `json:"external"`
}

// listRouters returns every registered router so the FE can build the switcher.
func listRouters(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	out := make([]routerInfo, 0, len(List()))
	for _, rt := range List() {
		out = append(out, routerInfo{
			ID:        rt.Desc.ID,
			Name:      rt.Desc.DisplayName,
			Blurb:     rt.Desc.Blurb,
			Icon:      rt.Desc.IconSVG,
			Autostart: store.GetAutostart(rt.Desc.ID),
			External:  store.GetExternalAPI(rt.Desc.ID),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"routers": out})
}

func setAutostart(id string) func(*Manager, *tool.Ctx) {
	return func(_ *Manager, c *tool.Ctx) {
		on := c.Form("on") == "true"
		if err := store.SetAutostart(c.Context(), id, on); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, map[string]bool{"autostart": on})
	}
}

func setExternal(id string) func(*Manager, *tool.Ctx) {
	return func(_ *Manager, c *tool.Ctx) {
		on := c.Form("on") == "true"
		if err := store.SetExternalAPI(c.Context(), id, on); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, map[string]bool{"external": on})
	}
}

// ── root-mounted proxy handlers (wired in server.go) ─────────────────

// masterOn reports whether the airouter master switch is on. Fail-closed
// when the store is unwired.
func masterOn() bool { return store != nil && store.Enabled() }

// RedirectTargetFor returns the wick-root redirect target for a request that
// escaped an embedded router's mount — a root-absolute app route (e.g. /home)
// that a router SPA navigated to client-side, which would otherwise 404 at the
// wick root (directly, or relayed by wick's service worker). It resolves which
// router the request belongs to from the Referer (…/airouter/<id>/…), falling
// back to the last-viewed dashboard router, then checks the path against that
// router's declared RoutePrefixes. Returns "" when nothing matches, so normal
// wick routing / 404s are unaffected. server.go 302-redirects to the result.
func RedirectTargetFor(path, referer string) string {
	if !masterOn() {
		return ""
	}
	id := routerIDFromReferer(referer)
	if id == "" {
		id = ActiveAssetRouter()
	}
	rt, ok := Get(id)
	if !ok {
		return ""
	}
	for _, p := range rt.Desc.RoutePrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return rt.Mgr.MountPrefix() + path
		}
	}
	return ""
}

// routerIDFromReferer extracts <id> from a referer URL containing
// "/airouter/<id>/…" (or ".../airouter/<id>"). Empty when absent — e.g. a
// service-worker-relayed request whose referer is /sw.js, which then falls back
// to the active-asset router.
func routerIDFromReferer(ref string) string {
	const marker = "/airouter/"
	i := strings.Index(ref, marker)
	if i < 0 {
		return ""
	}
	rest := ref[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}
	return rest
}

// RootProxy returns the dashboard reverse-proxy handler for router id, mounted
// at the wick root (/airouter/<id>/) behind admin auth in server.go. 404s when
// the master switch is off or the id is unknown.
func RootProxy(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := Get(id)
		if !ok || !masterOn() {
			http.NotFound(w, r)
			return
		}
		rt.Mgr.ProxyHandler().ServeHTTP(w, r)
	})
}

// APIProxy returns the OpenAI-compatible API reverse-proxy handler for router
// id, mounted UNAUTHENTICATED at /airouter/<id>/v1/. 404s when off/unknown.
func APIProxy(id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := Get(id)
		if !ok || !masterOn() {
			http.NotFound(w, r)
			return
		}
		rt.Mgr.APIProxyHandler().ServeHTTP(w, r)
	})
}

// NextAssetProxy handles bare root-absolute /_next/* asset requests that a
// router's Next.js bundle assembles at runtime and which slip past the body
// rewriter. It re-roots the path under the active router's prefix (the one
// whose dashboard HTML was last served) and hands it to that router's proxy.
// Admin auth is applied at the mount in server.go.
func NextAssetProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !masterOn() {
			http.NotFound(w, r)
			return
		}
		rt, ok := Get(ActiveAssetRouter())
		if !ok {
			http.NotFound(w, r)
			return
		}
		prefix := rt.Mgr.MountPrefix()
		r.URL.Path = prefix + r.URL.Path
		if r.URL.RawPath != "" {
			r.URL.RawPath = prefix + r.URL.RawPath
		}
		rt.Mgr.ProxyHandler().ServeHTTP(w, r)
	})
}

// ── boot / shutdown hooks ────────────────────────────────────────────

// Autostart starts each router whose stored auto-start flag is on and whose
// package is installed. Safe to call in a goroutine at boot.
func Autostart(logf func(string)) {
	if store == nil || !store.Enabled() {
		return
	}
	for _, rt := range List() {
		if !store.GetAutostart(rt.Desc.ID) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		if !rt.Mgr.Installed(ctx) {
			if logf != nil {
				logf("airouter: " + rt.Desc.ID + " autostart on but not installed — skipping")
			}
			cancel()
			continue
		}
		if err := rt.Mgr.StartAndWait(ctx); err != nil && logf != nil {
			logf("airouter: " + rt.Desc.ID + " autostart failed: " + err.Error())
		}
		cancel()
	}
}

// AnyAutostartEnabled reports whether at least one router has auto-start on, so
// server.go can decide whether to add a boot-gate step.
func AnyAutostartEnabled() bool {
	if store == nil || !store.Enabled() {
		return false
	}
	for _, rt := range List() {
		if store.GetAutostart(rt.Desc.ID) {
			return true
		}
	}
	return false
}

// StopAll kills every running router process. Exposed for shutdown hooks.
func StopAll() {
	for _, rt := range List() {
		rt.Mgr.StopProcess()
	}
}
