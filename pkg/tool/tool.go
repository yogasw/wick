// Package tool defines the public contract every tool module must
// satisfy. Downstream projects implement Module to register a tool
// with wick via wick/app.RegisterTool.
//
// A module exposes metadata via Meta() and wires its handlers via
// Register(r Router). Wick owns the HTTP mux, the page renderer, and
// the static-asset mount — the module only declares routes through
// the Router and reacts to requests via *Ctx.
package tool

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/entity"
)

// Tool describes a tool entry shown on the home page.
type Tool struct {
	// Key is the path segment that identifies this instance. Must be
	// unique across every Tool returned by every module — wick derives
	// the mount path from it as "/tools/{Key}". Lowercase slug, no
	// slashes. Multi-instance modules emit one Tool per Key.
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Icon is a short label (e.g. "Aa", "{}") shown inside the icon box.
	Icon string `json:"icon"`
	// Path is the fully resolved mount path ("/tools/{Key}"). Wick fills
	// this in at boot — modules must leave it zero.
	Path     string `json:"path"`
	Category string `json:"category,omitempty"`
	// DefaultVisibility is the visibility used when no DB override exists.
	// Defaults to VisibilityPrivate if not set.
	DefaultVisibility entity.ToolVisibility `json:"-"`
	// ExternalURL marks the tool as an external link. When set, the home
	// card and palette open this URL in a new tab instead of navigating
	// to Path. Direct hits to Path still resolve (the module redirects)
	// so the link can be shared, bookmarked, and gated by the visibility
	// middleware.
	ExternalURL string `json:"external_url,omitempty"`
	// DefaultTags are seeded on startup. Each tag is created globally by
	// name if missing (existing tags keep their flags untouched). The
	// tags are also linked to this tool on the *first* registration only
	// — once any tool_tag row exists for the tool path, the seed step
	// skips linking so an admin who unlinks a tag won't see it return on
	// the next restart.
	DefaultTags []DefaultTag `json:"-"`
}

// DefaultTag is the spec used by Tool.DefaultTags to seed tags on startup.
type DefaultTag struct {
	Name        string
	Description string
	IsGroup     bool
	IsFilter    bool
	SortOrder   int
}

// HandlerFunc is the tool-side handler signature. A handler receives a
// *Ctx that exposes request helpers (Form, Query, BindJSON) and
// response helpers (HTML, JSON, Redirect). Write to Ctx.W only when a
// helper doesn't fit — the helpers handle the render shell, content
// types, and status codes for you.
type HandlerFunc func(c *Ctx)

// Router is the surface wick exposes to a module's Register method.
// Modules declare their HTTP routes through these Echo/Gin-style
// verb methods; wick owns the underlying mux, validates that no two
// modules claim the same `METHOD PATH`, and mounts each handler with
// the page renderer already injected into *Ctx.
//
// Paths passed to GET/POST/... are relative to the meta's /tools/{Key}
// base; wick prefixes them at mount time. Static mounts a read-only
// fs.FS at prefix — typically "/static/" for the module's embed.FS of
// js/css assets. Directory listings are blocked.
//
// Meta returns the tool.Tool currently being registered. Register is
// invoked once per Meta entry; use this when a handler needs the base
// URL (form actions, script src) or the tool's display metadata.
type Router interface {
	GET(path string, h HandlerFunc)
	POST(path string, h HandlerFunc)
	PUT(path string, h HandlerFunc)
	DELETE(path string, h HandlerFunc)
	PATCH(path string, h HandlerFunc)
	Static(prefix string, fsys fs.FS)
	Meta() Tool
}

// StaticHandler serves files from fsys under the given URL prefix.
// Directory listings return 404 so embedded asset trees don't leak.
// Wick uses this internally for Router.Static; modules never call it
// directly.
func StaticHandler(prefix string, fsys fs.FS) http.Handler {
	fileSrv := http.StripPrefix(prefix, http.FileServer(http.FS(noDirFS{fsys})))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fileSrv.ServeHTTP(w, r)
	})
}

// noDirFS wraps fs.FS and hides directories from http.FileServer so
// the default index listing is never rendered.
type noDirFS struct{ fsys fs.FS }

func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}
