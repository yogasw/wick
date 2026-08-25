package tool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// WebhookHandlerFunc is the handler signature for routes declared on a
// WebhookRouter. It receives a *WebhookCtx rather than a *Ctx: the
// webhook surface is JSON-only, so the HTML render shell is not merely
// discouraged there, it is absent from the type.
type WebhookHandlerFunc func(c *WebhookCtx)

// WebhookRouter declares routes inside a tool's webhook subtree — an
// unauthenticated, JSON-only slice of the tool's mount point.
//
// Obtain one from Router.WebhookGroup(prefix). Paths passed to the verb
// methods are relative to that prefix, which is itself relative to the
// tool's /tools/{Key} base:
//
//	wh := r.WebhookGroup("/webhook")
//	wh.POST("/hook", receive)   // POST /tools/{Key}/webhook/hook
//
// Every route declared here bypasses wick's per-tool access check. The
// tool's own visibility (Public/Private) and filter tags do not apply:
// an inbound request reaches the handler with no session, no user, and
// no login redirect. That is the point — webhook senders cannot follow
// a 302 to /auth/login — but it means **the handler owns authentication
// entirely**. Verify a signature or shared secret against c.Cfg before
// acting on the payload:
//
//	func receive(c *tool.WebhookCtx) {
//	    if c.Header("X-Hook-Sig") != c.Cfg("secret") {
//	        c.JSON(http.StatusUnauthorized, map[string]string{"error": "bad signature"})
//	        return
//	    }
//	    ...
//	}
//
// A group with no such check is an endpoint anyone on the network can
// POST to. Sometimes that is intended; it is never accidental.
type WebhookRouter interface {
	GET(path string, h WebhookHandlerFunc)
	POST(path string, h WebhookHandlerFunc)
	PUT(path string, h WebhookHandlerFunc)
	DELETE(path string, h WebhookHandlerFunc)
	PATCH(path string, h WebhookHandlerFunc)
}

// WebhookCtx is the per-request handle passed to every
// WebhookHandlerFunc. It carries the request-reading and config helpers
// of Ctx but exposes only JSON, status-code, and raw responses — there
// is no HTML, Redirect, or styled-404 method, because a webhook caller
// is a program, not a browser.
//
// Drop down to WebhookCtx.W / WebhookCtx.R when a helper does not fit.
type WebhookCtx struct {
	W http.ResponseWriter
	R *http.Request
	// meta is the tool.Tool this route belongs to, captured at mount
	// time. Read via Meta() / Base(); scopes Cfg lookups to this key.
	meta Tool
	// cfg resolves runtime-editable config values. nil when the module
	// declared no Configs — Cfg/Missing then return zero values.
	cfg ConfigReader
}

// NewWebhookCtx is used by wick when mounting webhook handlers. Modules
// never call it directly — they receive a *WebhookCtx ready to use.
func NewWebhookCtx(w http.ResponseWriter, r *http.Request, meta Tool, cfg ConfigReader) *WebhookCtx {
	return &WebhookCtx{W: w, R: r, meta: meta, cfg: cfg}
}

// ── Request helpers ──────────────────────────────────────────────────

// Query returns the URL query value for key.
func (c *WebhookCtx) Query(key string) string { return c.R.URL.Query().Get(key) }

// Header returns the first value of the named request header, or "".
// Webhook senders carry their signature and content negotiation here,
// so this is the common read.
func (c *WebhookCtx) Header(key string) string { return c.R.Header.Get(key) }

// PathValue returns a Go 1.22+ mux path parameter (e.g. "/hook/{id}").
func (c *WebhookCtx) PathValue(key string) string { return c.R.PathValue(key) }

// Method returns the request method. Useful when one handler is wired
// to several verbs.
func (c *WebhookCtx) Method() string { return c.R.Method }

// BindJSON decodes the request body into v. Returns the decoder error
// verbatim so the caller can surface it.
//
// The body is consumed. When the handler also needs the raw bytes — to
// compute an HMAC over them, as most signed webhooks require — call
// Body first and unmarshal from the returned slice instead:
//
//	raw, err := c.Body()
//	if !validSig(raw, c.Header("X-Hook-Sig"), c.Cfg("secret")) { ... }
//	json.Unmarshal(raw, &payload)
func (c *WebhookCtx) BindJSON(v any) error {
	return json.NewDecoder(c.R.Body).Decode(v)
}

// Body reads and returns the whole request body. Prefer it over
// BindJSON when the payload must be verified before it is trusted:
// signature schemes sign the exact bytes, so they have to be read
// before any decoding.
func (c *WebhookCtx) Body() ([]byte, error) {
	return io.ReadAll(c.R.Body)
}

// Context is a shortcut for c.R.Context(); use it for cancellation-
// aware calls into services and repositories.
func (c *WebhookCtx) Context() context.Context { return c.R.Context() }

// Meta returns the tool.Tool this route was mounted under.
func (c *WebhookCtx) Meta() Tool { return c.meta }

// Base returns the absolute mount path for this tool ("/tools/{Key}").
func (c *WebhookCtx) Base() string { return c.meta.Path }

// Cfg returns the current value of a config row declared by this tool,
// scoped to the active instance's Key. Returns "" when the key is not
// declared or the config service is unavailable.
//
// This is where a webhook's shared secret or signing key lives: declare
// it as a `wick:"secret;required"` field so an admin can set and rotate
// it from /manager/tools/{Key} without a redeploy.
func (c *WebhookCtx) Cfg(key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(c.meta.Key, key)
}

// CfgOf reads a config value from another owner (another tool or a job
// key). Intentionally verbose — prefer Cfg for the common case.
func (c *WebhookCtx) CfgOf(owner, key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(owner, key)
}

// CfgInt returns c.Cfg(key) parsed as int. Unparseable or empty values
// return 0.
func (c *WebhookCtx) CfgInt(key string) int {
	n, _ := strconv.Atoi(c.Cfg(key))
	return n
}

// CfgBool returns c.Cfg(key) parsed as bool. "true"/"1"/"yes"/"on"
// (case-insensitive) count as true; anything else is false.
func (c *WebhookCtx) CfgBool(key string) bool {
	b, err := strconv.ParseBool(c.Cfg(key))
	return err == nil && b
}

// ConfigReader returns the underlying ConfigReader so callers that need
// to capture it beyond the request lifecycle can store it. Returns nil
// when no config service is wired.
func (c *WebhookCtx) ConfigReader() ConfigReader { return c.cfg }

// Missing returns the names of Required config rows this tool declared
// that have no stored value yet. A webhook handler should treat a
// non-empty result as "not configured" and refuse the request rather
// than process it unverified.
func (c *WebhookCtx) Missing() []string {
	if c.cfg == nil {
		return nil
	}
	return c.cfg.Missing(c.meta.Key)
}

// ── Response helpers ─────────────────────────────────────────────────

// JSON writes v as application/json with the given status code.
func (c *WebhookCtx) JSON(status int, v any) {
	c.W.Header().Set("Content-Type", "application/json")
	c.W.WriteHeader(status)
	_ = json.NewEncoder(c.W).Encode(v)
}

// Status writes a bare status code with no body. Use for the "received,
// nothing to say" reply that many webhook senders expect (204, or 200
// with an empty body).
func (c *WebhookCtx) Status(status int) { c.W.WriteHeader(status) }

// Error writes a JSON error object: {"error": msg}. Unlike Ctx.Error,
// which writes plain text, this stays JSON so a webhook caller can
// parse failures the same way it parses successes.
func (c *WebhookCtx) Error(status int, msg string) {
	c.JSON(status, map[string]string{"error": msg})
}

// WebhookRoute describes one route declared inside a webhook group.
// Wick collects these at boot and surfaces them on the tool's admin
// settings page: a webhook endpoint answers without a login, so an
// operator needs to be able to see which URLs are open without reading
// the module source.
type WebhookRoute struct {
	// ToolKey is the Meta.Key of the tool that declared the route.
	ToolKey string
	// Method is the HTTP verb ("POST", "PUT", ...).
	Method string
	// Path is the absolute mounted path, e.g. "/tools/myhook/webhook/hook".
	Path string
	// Group is the prefix passed to WebhookGroup, resolved absolute —
	// several routes normally share one group.
	Group string
}
