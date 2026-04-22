package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
)

// RenderFunc wraps a body fragment in wick's page shell (navbar,
// layout, theme) and writes the full HTML response. Wick injects it
// into *Ctx; modules call it indirectly via Ctx.HTML. The *Ctx hand-
// off lets the renderer reach c.Missing() / c.Meta() without extra
// threading from the router.
type RenderFunc func(c *Ctx, body templ.Component)

// ConfigReader is the narrow slice of the config service wick exposes
// to tool handlers. Scoping by owner happens in Ctx — handlers see
// only their own tool's values. Implementations live in internal/
// configs; this interface keeps pkg/tool free of internal imports.
type ConfigReader interface {
	GetOwned(owner, key string) string
	Missing(owner string) []string
}

// Ctx is the per-request handle passed to every HandlerFunc. It bundles
// the raw http.ResponseWriter and *http.Request with wick-supplied
// helpers for the things a tool handler does constantly: read form
// values, decode JSON bodies, render HTML, write JSON, redirect.
//
// Drop down to Ctx.W / Ctx.R when you need something a helper does not
// expose — the helpers are shortcuts, not a wall.
type Ctx struct {
	W http.ResponseWriter
	R *http.Request
	// render is injected by wick when a GET/POST/... handler is mounted.
	// Never nil inside a handler; HTML panics otherwise, which is the
	// right signal during development.
	render RenderFunc
	// meta is the tool.Tool this route belongs to, captured at mount
	// time. Read via Meta() / Base() so handlers can build URLs without
	// hardcoding /tools/{Key}.
	meta Tool
	// cfg resolves runtime-editable config values. nil when no module
	// declared Specs — in that case Cfg/Missing return zero values.
	cfg ConfigReader
}

// NewCtx is used by wick when mounting handlers. Modules never call it
// directly — they receive a *Ctx ready to use.
func NewCtx(w http.ResponseWriter, r *http.Request, render RenderFunc, meta Tool, cfg ConfigReader) *Ctx {
	return &Ctx{W: w, R: r, render: render, meta: meta, cfg: cfg}
}

// ── Request helpers ──────────────────────────────────────────────────

// Form returns r.FormValue(key). Works for both url-encoded bodies and
// multipart forms. Empty string when the key is missing.
func (c *Ctx) Form(key string) string { return c.R.FormValue(key) }

// Query returns the URL query value for key.
func (c *Ctx) Query(key string) string { return c.R.URL.Query().Get(key) }

// PathValue returns a Go 1.22+ mux path parameter (e.g. "/items/{id}").
func (c *Ctx) PathValue(key string) string { return c.R.PathValue(key) }

// BindJSON decodes the request body into v. Returns the decoder error
// verbatim so the caller can surface it.
func (c *Ctx) BindJSON(v any) error {
	return json.NewDecoder(c.R.Body).Decode(v)
}

// Context is a shortcut for c.R.Context(); use it for cancellation-
// aware calls into services and repositories.
func (c *Ctx) Context() context.Context { return c.R.Context() }

// Meta returns the tool.Tool this route was mounted under. Handlers
// use it to read display metadata (Name, Icon) or ExternalURL without
// threading anything through closures.
func (c *Ctx) Meta() Tool { return c.meta }

// Base returns the absolute mount path for this tool ("/tools/{Key}").
// Use it for form actions, script src, and redirect targets so HTML
// works regardless of how many instances of the module are registered.
func (c *Ctx) Base() string { return c.meta.Path }

// Cfg returns the current value of a Spec declared by this tool. The
// lookup is scoped to the active instance's Key — reading another
// tool's config requires CfgOf. Returns "" when the key is not
// declared or the config service is unavailable.
func (c *Ctx) Cfg(key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(c.meta.Key, key)
}

// CfgOf reads a config value from another owner (another tool or a
// job key). Intentionally verbose — reserved for cross-tool
// integrations that need a neighbor's endpoint or shared identifier.
// Prefer Cfg for the common case.
func (c *Ctx) CfgOf(owner, key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(owner, key)
}

// CfgInt returns c.Cfg(key) parsed as int. Unparseable or empty
// values return 0 — handlers that need to distinguish "unset" from
// "zero" should mark the field Required and check c.Missing() first.
func (c *Ctx) CfgInt(key string) int {
	n, _ := strconv.Atoi(c.Cfg(key))
	return n
}

// CfgBool returns c.Cfg(key) parsed as bool. "true"/"1"/"yes"/"on"
// (case-insensitive) count as true; anything else is false.
func (c *Ctx) CfgBool(key string) bool {
	b, err := strconv.ParseBool(c.Cfg(key))
	return err == nil && b
}

// Missing returns the names of Required Specs this tool declared that
// have no stored value yet. Handlers call it at the top of a request
// to decide whether to render the real view or a "setup required"
// banner. Returns nil when nothing is required or the config service
// is unavailable.
func (c *Ctx) Missing() []string {
	if c.cfg == nil {
		return nil
	}
	return c.cfg.Missing(c.meta.Key)
}

// ── Response helpers ─────────────────────────────────────────────────

// HTML renders body inside wick's page shell and writes the full HTML
// response. Use for any tool page that lives under /tools/...
func (c *Ctx) HTML(body templ.Component) { c.render(c, body) }

// JSON writes v as application/json with the given status code.
func (c *Ctx) JSON(status int, v any) {
	c.W.Header().Set("Content-Type", "application/json")
	c.W.WriteHeader(status)
	_ = json.NewEncoder(c.W).Encode(v)
}

// Redirect issues an HTTP redirect. Code is typically http.StatusFound
// (302) for user actions or http.StatusSeeOther (303) after a POST.
func (c *Ctx) Redirect(url string, code int) {
	http.Redirect(c.W, c.R, url, code)
}

// NotFound writes a 404 with no body.
func (c *Ctx) NotFound() { http.NotFound(c.W, c.R) }

// Error writes an error response with the given status code and
// message. Messages are plain text; use JSON for structured errors.
func (c *Ctx) Error(status int, msg string) {
	http.Error(c.W, msg, status)
}
