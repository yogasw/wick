package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
	// notFound renders the app-wide 404 page. Injected at mount time so
	// c.NotFound() produces the same styled page as the auth middleware.
	notFound func(w http.ResponseWriter, r *http.Request)
	// meta is the tool.Tool this route belongs to, captured at mount
	// time. Read via Meta() / Base() so handlers can build URLs without
	// hardcoding /tools/{Key}.
	meta Tool
	// cfg resolves runtime-editable config values. nil when no module
	// declared Specs — in that case Cfg/Missing return zero values.
	cfg ConfigReader
}

// User is the signed-in person behind a tool request — the slice of
// wick's user record a tool has any business seeing. Never a password
// hash, never the whole row.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
	// Tags are the names of the access tags granted to this user — the
	// same tags an admin manages at /admin/tags. A tool uses them to
	// answer "may this person change things", which wick's own
	// visibility check cannot express: that check is per-tool, and a
	// tool that everyone may READ often still has writes worth gating.
	Tags []string `json:"tags,omitempty"`
}

// HasTag reports whether the user carries a tag, matched
// case-insensitively so a config saying "support" finds a tag named
// "Support". An admin is not given a free pass here — a tool that wants
// one should say so itself.
func (u User) HasTag(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, t := range u.Tags {
		if strings.EqualFold(strings.TrimSpace(t), name) {
			return true
		}
	}
	return false
}

// UserResolver reads the signed-in user off a request.
//
// It is installed by wick at boot rather than threaded through NewCtx,
// for two reasons: NewCtx is public API that downstream code already
// calls, and the session layer lives in internal/ where pkg/tool cannot
// import it. With no resolver installed Ctx.User reports nobody — the
// right answer for a binary with no session layer wired, and never a
// panic on a path a tool may reach during tests.
type UserResolver func(r *http.Request) (User, bool)

var userResolver UserResolver

// SetUserResolver installs the resolver. Called once during boot; calling
// it again replaces the previous one.
func SetUserResolver(fn UserResolver) { userResolver = fn }

// TokenResolver authenticates a wick Personal Access Token into the user
// that owns it. Installed by wick at boot, for the same reasons as
// UserResolver: the token store lives in internal/.
//
// This is what lets a tool's machine surface accept a credential a person
// can create for themselves and paste into any HTTP client, without that
// tool inventing its own token scheme — and without losing WHO is calling,
// which a shared secret always does.
type TokenResolver func(ctx context.Context, token string) (User, bool)

var tokenResolver TokenResolver

// SetTokenResolver installs the PAT resolver.
func SetTokenResolver(fn TokenResolver) { tokenResolver = fn }

// ResolveToken authenticates a wick Personal Access Token. ok is false for
// an unknown, revoked, or expired token, and whenever no resolver is
// installed — so a missing session layer denies rather than admits.
func ResolveToken(ctx context.Context, token string) (User, bool) {
	if tokenResolver == nil || token == "" {
		return User{}, false
	}
	return tokenResolver(ctx, token)
}

// NewCtx is used by wick when mounting handlers. Modules never call it
// directly — they receive a *Ctx ready to use.
func NewCtx(w http.ResponseWriter, r *http.Request, render RenderFunc, meta Tool, cfg ConfigReader, notFound func(http.ResponseWriter, *http.Request)) *Ctx {
	return &Ctx{W: w, R: r, render: render, meta: meta, cfg: cfg, notFound: notFound}
}

// ── Request helpers ──────────────────────────────────────────────────

// Form returns r.FormValue(key). Works for both url-encoded bodies and
// multipart forms. Empty string when the key is missing.
func (c *Ctx) Form(key string) string { return c.R.FormValue(key) }

// Query returns the URL query value for key.
func (c *Ctx) Query(key string) string { return c.R.URL.Query().Get(key) }

// WantsJSON returns true when the client signals it wants JSON over HTML:
// Accept header contains "application/json" OR the `format=json` query
// param is set. Dual-mode handlers branch on this so the same VM struct
// powers both the templ render and the SPA fetch.
func (c *Ctx) WantsJSON() bool {
	if c.Query("format") == "json" {
		return true
	}
	for _, a := range c.R.Header.Values("Accept") {
		// Cheap substring — handlers don't negotiate quality scores.
		// "application/json" wins regardless of media-type parameters.
		for i := 0; i+len("application/json") <= len(a); i++ {
			if a[i:i+len("application/json")] == "application/json" {
				return true
			}
		}
	}
	return false
}

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

// User returns the signed-in user behind this request. ok is false when
// the route was reached without a session — a public tool viewed by a
// logged-out visitor, or a binary with no session layer.
//
// Use it to answer "what is MINE" without asking the person to identify
// themselves in a form they can get wrong. Do not use it for access
// control: wick has already applied the tool's visibility and tag rules
// before the handler runs.
func (c *Ctx) User() (User, bool) {
	if userResolver == nil || c.R == nil {
		return User{}, false
	}
	return userResolver(c.R)
}

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

// CfgBool returns c.Cfg(key) parsed as bool via strconv.ParseBool, so
// "1", "t", "T", "TRUE", "true", "True" count as true. Anything else —
// including "yes" and "on", which ParseBool rejects — is false.
//
// The widgets that write these rows store "true"/"false", so the narrow
// set is what config values actually hold; the parser is deliberately not
// widened, since doing so would flip existing rows that read as false
// today.
func (c *Ctx) CfgBool(key string) bool {
	b, err := strconv.ParseBool(c.Cfg(key))
	return err == nil && b
}

// ConfigReader returns the underlying ConfigReader so callers that need
// to capture it for use outside the request lifecycle (e.g. background
// workers) can store it. Returns nil when no config service is wired.
func (c *Ctx) ConfigReader() ConfigReader { return c.cfg }

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

// NotFound renders the app-wide styled 404 page.
func (c *Ctx) NotFound() {
	if c.notFound != nil {
		c.notFound(c.W, c.R)
		return
	}
	http.NotFound(c.W, c.R)
}

// Error writes an error response with the given status code and
// message. Messages are plain text; use JSON for structured errors.
func (c *Ctx) Error(status int, msg string) {
	http.Error(c.W, msg, status)
}
