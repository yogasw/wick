package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/pkg/render"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/pkg/tool"
)

// toolRouter collects the routes declared by every tool module and
// mounts them on a single *http.ServeMux once all modules have
// registered. Collecting first lets wick fail the boot with a clear
// error if two modules (or two instances of the same module) claim the
// same "METHOD PATH" — a class of bug that would otherwise manifest as
// a silent last-write-wins at mux.Handle.
//
// Modules declare paths relative to their meta's /tools/{Key} mount
// point; the current meta is set by withScope before each per-meta
// Register call and the router prefixes paths from meta.Key at add
// time. Modules can read the active meta via Router.Meta() without
// having to thread it through the interface.
//
// Render is cached per route at declaration time so the per-request
// hot path does not look it up again.
type toolRouter struct {
	// meta is the tool currently being registered. Set by withScope
	// before a module's Register runs and cleared after. Consumed by
	// Meta() and by path resolution (meta.Key -> /tools/{key}).
	meta tool.Tool
	// hasConfigs is true when the module currently being registered
	// declared at least one Config row. The renderer uses it to show
	// the admin gear shortcut only when there's something to manage.
	hasConfigs bool
	// cfg is injected into every Ctx so handlers can read their own
	// declared Specs via c.Cfg / c.Missing without threading the
	// service through closures.
	cfg tool.ConfigReader

	routes  []routeEntry
	statics []staticEntry
	raws    []rawEntry
	mws     []mwEntry
	hooks   []hookEntry
}

type routeEntry struct {
	method, path, owner string
	h                   tool.HandlerFunc
	render              tool.RenderFunc
	meta                tool.Tool
}

type staticEntry struct {
	prefix, owner string
	fsys          fs.FS
}

type rawEntry struct {
	prefix, owner string
	fn            func(cfg tool.ConfigReader) http.Handler
}

type mwEntry struct {
	prefix string // resolved, /tools/{key}-prefixed
	owner  string
	mw     tool.Middleware
}

// hookEntry is one route declared on a WebhookGroup. These mount on the
// same toolsMux as ordinary routes but their resolved paths are also
// reported via WebhookPrefixes so server.go can route them around the
// per-tool access check.
type hookEntry struct {
	method, path, owner, group string
	toolKey                    string
	h                          tool.WebhookHandlerFunc
	meta                       tool.Tool
}

func newToolRouter(cfg tool.ConfigReader) *toolRouter {
	return &toolRouter{cfg: cfg}
}

// withScope runs fn with the router scoped to one meta. All routes and
// statics fn registers inherit the meta for error reporting and are
// mounted under /tools/{meta.Key}. hasConfigs is captured on every
// route so the renderer can decide whether to show the admin gear.
func (t *toolRouter) withScope(meta tool.Tool, hasConfigs bool, fn func(r tool.Router)) {
	prevMeta, prevHas := t.meta, t.hasConfigs
	t.meta, t.hasConfigs = meta, hasConfigs
	fn(t)
	t.meta, t.hasConfigs = prevMeta, prevHas
}

// ── tool.Router implementation ───────────────────────────────────────

func (t *toolRouter) GET(path string, h tool.HandlerFunc)    { t.add("GET", path, h) }
func (t *toolRouter) POST(path string, h tool.HandlerFunc)   { t.add("POST", path, h) }
func (t *toolRouter) PUT(path string, h tool.HandlerFunc)    { t.add("PUT", path, h) }
func (t *toolRouter) DELETE(path string, h tool.HandlerFunc) { t.add("DELETE", path, h) }
func (t *toolRouter) PATCH(path string, h tool.HandlerFunc)  { t.add("PATCH", path, h) }

func (t *toolRouter) Use(prefix string, mw tool.Middleware) {
	if mw == nil {
		return
	}
	t.mws = append(t.mws, mwEntry{
		prefix: t.resolve(prefix),
		owner:  t.meta.Name,
		mw:     mw,
	})
}

func (t *toolRouter) Static(prefix string, fsys fs.FS) {
	t.statics = append(t.statics, staticEntry{
		prefix: t.resolve(prefix),
		owner:  t.meta.Name,
		fsys:   fsys,
	})
}

func (t *toolRouter) HandleRaw(prefix string, fn func(cfg tool.ConfigReader) http.Handler) {
	t.raws = append(t.raws, rawEntry{
		prefix: t.resolve(prefix),
		owner:  t.meta.Name,
		fn:     fn,
	})
}

// WebhookGroup opens an unauthenticated JSON-only subtree at prefix and
// returns a router scoped to it. The prefix is resolved against the
// current meta's /tools/{key} base immediately, so the returned router
// keeps working after withScope moves on to the next module.
func (t *toolRouter) WebhookGroup(prefix string) tool.WebhookRouter {
	return &webhookRouter{
		parent: t,
		group:  t.resolve(prefix),
		meta:   t.meta,
	}
}

// webhookRouter implements tool.WebhookRouter by appending to its
// parent's hooks slice. It captures the meta at construction time
// rather than reading t.meta per call, so a module may hold the group
// past the end of its own Register without picking up another tool's
// scope.
type webhookRouter struct {
	parent *toolRouter
	group  string // resolved, /tools/{key}-prefixed
	meta   tool.Tool
}

func (g *webhookRouter) GET(path string, h tool.WebhookHandlerFunc) { g.add("GET", path, h) }
func (g *webhookRouter) POST(path string, h tool.WebhookHandlerFunc) {
	g.add("POST", path, h)
}
func (g *webhookRouter) PUT(path string, h tool.WebhookHandlerFunc) { g.add("PUT", path, h) }
func (g *webhookRouter) DELETE(path string, h tool.WebhookHandlerFunc) {
	g.add("DELETE", path, h)
}
func (g *webhookRouter) PATCH(path string, h tool.WebhookHandlerFunc) {
	g.add("PATCH", path, h)
}

// add joins the group prefix with a route-relative path and records the
// entry on the parent router.
func (g *webhookRouter) add(method, path string, h tool.WebhookHandlerFunc) {
	if h == nil {
		return
	}
	g.parent.hooks = append(g.parent.hooks, hookEntry{
		method:  method,
		path:    joinPath(g.group, path),
		owner:   g.meta.Name,
		group:   g.group,
		toolKey: g.meta.Key,
		h:       h,
		meta:    g.meta,
	})
}

// joinPath appends a route-relative path to an already-resolved base.
// An empty or "/" rel means the base itself.
func joinPath(base, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "/" {
		return base
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return base + rel
}

// Meta returns the tool currently being registered. Useful when a
// handler needs the absolute base URL (form actions, script src) or
// other display metadata — avoids threading meta through the Register
// signature. Outside a Register scope it returns the zero Tool.
func (t *toolRouter) Meta() tool.Tool { return t.meta }

func (t *toolRouter) add(method, path string, h tool.HandlerFunc) {
	if h == nil {
		return
	}
	t.routes = append(t.routes, routeEntry{
		method: method,
		path:   t.resolve(path),
		owner:  t.meta.Name,
		h:      h,
		render: render.NewToolRenderer(t.hasConfigs),
		meta:   t.meta,
	})
}

// mwCovers reports whether a middleware registered at prefix applies to a
// route at path: true for the exact path and anything nested under it, matched
// on segment boundaries so "/sessions/{id}" covers "/sessions/{id}/send" but
// not a sibling like "/sessions/{id}x" or the shorter "/sessions". A route and
// a middleware that share a subtree share the same "{id}" wildcard names, so
// comparing the resolved path strings is exact and sufficient.
func mwCovers(prefix, path string) bool {
	if prefix == "" {
		return false
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// resolve joins the current /tools/{key} base with a module-supplied
// relative path. "/" means the base itself ("/tools/{key}"); every
// other value is appended verbatim after the base.
func (t *toolRouter) resolve(rel string) string {
	if t.meta.Key == "" {
		// Unscoped add — leave the path alone so validate() can flag it.
		return rel
	}
	base := "/tools/" + t.meta.Key
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "/" {
		return base
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return base + rel
}

// ── Validation & mount ───────────────────────────────────────────────

// validate reports the first duplicate "METHOD PATH" across collected
// routes, or an empty static prefix. Wick calls this before mount so
// misconfiguration fails the boot with a pointed message.
func (t *toolRouter) validate() error {
	seen := make(map[string]string) // "METHOD PATH" -> owner
	for _, r := range t.routes {
		if strings.TrimSpace(r.path) == "" {
			return fmt.Errorf("tool %q: %s handler has empty path", r.owner, r.method)
		}
		key := r.method + " " + r.path
		if prev, dup := seen[key]; dup {
			return fmt.Errorf("tool: duplicate route %s (owned by %q and %q)", key, prev, r.owner)
		}
		seen[key] = r.owner
	}
	// Webhook routes share the mux with ordinary routes, so they share
	// the duplicate check. Checking them in the same map also catches a
	// module that opens a webhook group over a path it already serves
	// as a normal (access-checked) route — that would silently strip the
	// access check off the earlier route, so it must fail the boot.
	for _, hk := range t.hooks {
		if strings.TrimSpace(hk.group) == "" {
			return fmt.Errorf("tool %q: WebhookGroup called with empty prefix", hk.owner)
		}
		if hk.group == "/tools/"+hk.toolKey {
			return fmt.Errorf("tool %q: WebhookGroup(%q) would expose the whole tool without authentication; use a sub-path such as \"/webhook\"", hk.owner, hk.group)
		}
		key := hk.method + " " + hk.path
		if prev, dup := seen[key]; dup {
			return fmt.Errorf("tool: duplicate route %s (owned by %q and %q)", key, prev, hk.owner)
		}
		seen[key] = hk.owner
	}
	for _, s := range t.statics {
		if strings.TrimSpace(s.prefix) == "" {
			return fmt.Errorf("tool %q: Static called with empty prefix", s.owner)
		}
	}
	for _, raw := range t.raws {
		if strings.TrimSpace(raw.prefix) == "" {
			return fmt.Errorf("tool %q: HandleRaw called with empty prefix", raw.owner)
		}
		if !strings.HasSuffix(raw.prefix, "/") {
			return fmt.Errorf("tool %q: HandleRaw prefix %q must end with '/'", raw.owner, raw.prefix)
		}
	}
	return nil
}

// mount wires every collected route and static mount onto mux.
//
// Tool root routes (path == "/tools/{key}") are registered twice — for
// every method — so "/tools/{key}" and "/tools/{key}/" both hit the same
// handler. Without this, ServeMux treats them as distinct patterns (bare
// = exact-match, trailing-slash = subtree-match), so a POST/DELETE to the
// trailing-slash root would 405 against a GET-only {$} pattern.
func (t *toolRouter) mount(mux *http.ServeMux) {
	for _, r := range t.routes {
		r := r
		cfg := t.cfg
		// Compose the middleware chain once at mount: wrap from last matching
		// to first so the first-registered middleware ends up outermost. The
		// per-request hot path then runs a pre-built chain, no matching.
		h := r.h
		for i := len(t.mws) - 1; i >= 0; i-- {
			if mwCovers(t.mws[i].prefix, r.path) {
				h = t.mws[i].mw(h)
			}
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			notFound := func(w http.ResponseWriter, r *http.Request) {
				ui.RenderNotFound(w, r, nil, http.StatusNotFound)
			}
			h(tool.NewCtx(w, req, r.render, r.meta, cfg, notFound))
		})
		mux.Handle(r.method+" "+r.path, handler)
		if r.path == "/tools/"+r.meta.Key {
			mux.Handle(r.method+" "+r.path+"/{$}", handler)
		}
	}
	for _, s := range t.statics {
		mux.Handle("GET "+s.prefix, tool.StaticHandler(s.prefix, s.fsys))
	}
	for _, raw := range t.raws {
		mux.Handle(raw.prefix, raw.fn(t.cfg))
	}
	for _, hk := range t.hooks {
		hk := hk
		cfg := t.cfg
		mux.Handle(hk.method+" "+hk.path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hk.h(tool.NewWebhookCtx(w, req, hk.meta, cfg))
		}))
	}
}

// WebhookPrefixes returns the distinct group prefixes opened via
// Router.WebhookGroup, sorted for a stable boot log and a stable admin
// listing. server.go routes requests under these prefixes straight to
// the tools mux, skipping the per-tool access check.
func (t *toolRouter) WebhookPrefixes() []string {
	seen := make(map[string]bool, len(t.hooks))
	out := make([]string, 0, len(t.hooks))
	for _, hk := range t.hooks {
		if seen[hk.group] {
			continue
		}
		seen[hk.group] = true
		out = append(out, hk.group)
	}
	sort.Strings(out)
	return out
}

// WebhookRoutes returns one descriptor per declared webhook route,
// grouped by tool key and sorted by path then method. The manager
// surfaces these on a tool's settings page so an operator can see the
// exact URLs that answer without a login.
func (t *toolRouter) WebhookRoutes() []tool.WebhookRoute {
	out := make([]tool.WebhookRoute, 0, len(t.hooks))
	for _, hk := range t.hooks {
		out = append(out, tool.WebhookRoute{
			ToolKey: hk.toolKey,
			Method:  hk.method,
			Path:    hk.path,
			Group:   hk.group,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}
