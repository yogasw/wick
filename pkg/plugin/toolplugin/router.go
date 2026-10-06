package toolplugin

import (
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/a-h/templ"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// router is the plugin-side tool.Router: the same route surface wick offers
// built-in tools, mounted on the plugin's own mux. Paths keep the full
// /tools/{key} prefix because the host forwards the request path unchanged.
type router struct {
	meta    tool.Tool
	cfg     tool.ConfigReader
	routes  []route
	mws     []mwEntry
	statics []staticEntry
	raws    []rawEntry
	hooks   []hookEntry
}

type route struct {
	method, path string
	h            tool.HandlerFunc
}

type mwEntry struct {
	prefix string
	mw     tool.Middleware
}

type staticEntry struct {
	prefix string
	fsys   fs.FS
}

type rawEntry struct {
	prefix string
	fn     func(cfg tool.ConfigReader) http.Handler
}

type hookEntry struct {
	method, path, group string
	h                   tool.WebhookHandlerFunc
}

func newRouter(meta tool.Tool, cfg tool.ConfigReader) *router {
	meta.Path = "/tools/" + meta.Key
	return &router{meta: meta, cfg: cfg}
}

func (t *router) GET(p string, h tool.HandlerFunc)    { t.add("GET", p, h) }
func (t *router) POST(p string, h tool.HandlerFunc)   { t.add("POST", p, h) }
func (t *router) PUT(p string, h tool.HandlerFunc)    { t.add("PUT", p, h) }
func (t *router) DELETE(p string, h tool.HandlerFunc) { t.add("DELETE", p, h) }
func (t *router) PATCH(p string, h tool.HandlerFunc)  { t.add("PATCH", p, h) }
func (t *router) Meta() tool.Tool                     { return t.meta }

func (t *router) add(method, p string, h tool.HandlerFunc) {
	if h != nil {
		t.routes = append(t.routes, route{method: method, path: t.resolve(p), h: h})
	}
}

func (t *router) Use(prefix string, mw tool.Middleware) {
	if mw != nil {
		t.mws = append(t.mws, mwEntry{prefix: t.resolve(prefix), mw: mw})
	}
}

func (t *router) Static(prefix string, fsys fs.FS) {
	t.statics = append(t.statics, staticEntry{prefix: t.resolve(prefix), fsys: fsys})
}

func (t *router) HandleRaw(prefix string, fn func(cfg tool.ConfigReader) http.Handler) {
	t.raws = append(t.raws, rawEntry{prefix: t.resolve(prefix), fn: fn})
}

func (t *router) WebhookGroup(prefix string) tool.WebhookRouter {
	return &hookRouter{parent: t, group: t.resolve(prefix)}
}

type hookRouter struct {
	parent *router
	group  string
}

func (g *hookRouter) GET(p string, h tool.WebhookHandlerFunc)    { g.add("GET", p, h) }
func (g *hookRouter) POST(p string, h tool.WebhookHandlerFunc)   { g.add("POST", p, h) }
func (g *hookRouter) PUT(p string, h tool.WebhookHandlerFunc)    { g.add("PUT", p, h) }
func (g *hookRouter) DELETE(p string, h tool.WebhookHandlerFunc) { g.add("DELETE", p, h) }
func (g *hookRouter) PATCH(p string, h tool.WebhookHandlerFunc)  { g.add("PATCH", p, h) }

func (g *hookRouter) add(method, p string, h tool.WebhookHandlerFunc) {
	if h == nil {
		return
	}
	full := g.group
	if p = strings.TrimSpace(p); p != "" && p != "/" {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		full += p
	}
	g.parent.hooks = append(g.parent.hooks, hookEntry{method: method, path: full, group: g.group, h: h})
}

// resolve mirrors wick's toolRouter: "/" is the base itself, anything else is
// appended after /tools/{key}.
func (t *router) resolve(rel string) string {
	base := t.meta.Path
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "/" {
		return base
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return base + rel
}

// webhooks lists the declared webhook routes relative to /tools/{key}, for
// the manifest. The host opens exactly these without a session.
func (t *router) webhooks() []wickplugin.ToolWebhook {
	out := make([]wickplugin.ToolWebhook, 0, len(t.hooks))
	for _, h := range t.hooks {
		out = append(out, wickplugin.ToolWebhook{
			Method: h.method,
			Path:   strings.TrimPrefix(h.path, t.meta.Path),
			Group:  strings.TrimPrefix(h.group, t.meta.Path),
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

// renderFragment is the plugin's tool.RenderFunc: instead of wick's page
// shell it writes the bare body and asks the host to wrap it.
func renderFragment(c *tool.Ctx, body templ.Component) {
	h := c.W.Header()
	h.Set(wickplugin.HeaderLayout, wickplugin.LayoutPage)
	h.Set(wickplugin.HeaderTitle, c.Meta().Name)
	h.Set("Content-Type", "text/html; charset=utf-8")
	_ = body.Render(c.R.Context(), c.W)
}

func covers(prefix, p string) bool {
	return prefix != "" && (p == prefix || strings.HasPrefix(p, prefix+"/"))
}

// handler mounts every collected route on a fresh mux.
func (t *router) handler() http.Handler {
	mux := http.NewServeMux()
	notFound := func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	for _, r := range t.routes {
		h := r.h
		for i := len(t.mws) - 1; i >= 0; i-- {
			if covers(t.mws[i].prefix, r.path) {
				h = t.mws[i].mw(h)
			}
		}
		meta, cfg := t.meta, t.cfg
		hf := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h(tool.NewCtx(w, req, renderFragment, meta, cfg, notFound))
		})
		mux.Handle(r.method+" "+r.path, hf)
		if r.path == t.meta.Path {
			mux.Handle(r.method+" "+r.path+"/{$}", hf)
		}
	}
	for _, s := range t.statics {
		mux.Handle("GET "+s.prefix, tool.StaticHandler(s.prefix, s.fsys))
	}
	for _, raw := range t.raws {
		mux.Handle(raw.prefix, raw.fn(t.cfg))
	}
	for _, hk := range t.hooks {
		hk, meta, cfg := hk, t.meta, t.cfg
		mux.Handle(hk.method+" "+hk.path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hk.h(tool.NewWebhookCtx(w, req, meta, cfg))
		}))
	}
	return mux
}
