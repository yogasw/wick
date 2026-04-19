package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/pkg/render"
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

func (t *toolRouter) Static(prefix string, fsys fs.FS) {
	t.statics = append(t.statics, staticEntry{
		prefix: t.resolve(prefix),
		owner:  t.meta.Name,
		fsys:   fsys,
	})
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
		render: render.NewToolRenderer(t.meta, t.hasConfigs),
		meta:   t.meta,
	})
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
	for _, s := range t.statics {
		if strings.TrimSpace(s.prefix) == "" {
			return fmt.Errorf("tool %q: Static called with empty prefix", s.owner)
		}
	}
	return nil
}

// mount wires every collected route and static mount onto mux.
func (t *toolRouter) mount(mux *http.ServeMux) {
	for _, r := range t.routes {
		r := r
		cfg := t.cfg
		mux.Handle(r.method+" "+r.path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.h(tool.NewCtx(w, req, r.render, r.meta, cfg))
		}))
	}
	for _, s := range t.statics {
		mux.Handle("GET "+s.prefix, tool.StaticHandler(s.prefix, s.fsys))
	}
}
