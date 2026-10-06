package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// Proxy forwards /tools/{key}/* to one tool plugin's unix socket.
type Proxy struct {
	key, base, version string
	runner             *Runner
	// hookGroups are the webhook group prefixes (absolute). Only the exact
	// webhook routes from the manifest are mounted without a session; any
	// other path under a group lands on the catch-all and must 404, because
	// wick routes whole group prefixes around the access check.
	hookGroups []string
}

// request is what one proxied call needs besides the raw request.
type request struct {
	user *tool.User               // nil = no session (webhook / public visitor)
	cfg  map[string]string        // the tool's current config
	wrap func(body []byte) []byte // wraps a page fragment in wick's layout; nil = never wrap
}

// ServeCtx proxies a session-gated tool route.
func (p *Proxy) ServeCtx(c *tool.Ctx, cfgKeys []string) {
	for _, g := range p.hookGroups {
		if c.R.URL.Path == g || strings.HasPrefix(c.R.URL.Path, g+"/") {
			http.NotFound(c.W, c.R) // not a declared webhook route
			return
		}
	}
	req := request{cfg: map[string]string{}}
	for _, k := range cfgKeys {
		req.cfg[k] = c.Cfg(k)
	}
	if u, ok := c.User(); ok {
		req.user = &u
	}
	req.wrap = func(body []byte) []byte {
		bw := &bufWriter{h: http.Header{}}
		cc := *c // carries wick's page renderer + meta
		cc.W = bw
		cc.HTML(rawHTML(body))
		return bw.buf.Bytes()
	}
	p.serve(c.W, c.R, req)
}

// ServeWebhook proxies a manifest-declared webhook route: no session, no
// user headers, never wrapped.
func (p *Proxy) ServeWebhook(c *tool.WebhookCtx, cfgKeys []string) {
	req := request{cfg: map[string]string{}}
	for _, k := range cfgKeys {
		req.cfg[k] = c.Cfg(k)
	}
	p.serve(c.W, c.R, req)
}

func (p *Proxy) etag() string { return `W/"` + p.key + "-" + p.version + `"` }

func (p *Proxy) isStatic(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/static/")
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, req request) {
	// Static assets only change with the plugin version: answer a matching
	// revalidation without waking the plugin.
	if p.isStatic(r) && r.Header.Get("If-None-Match") == p.etag() {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	rt, release, err := p.runner.Acquire(r.Context(), req.cfg)
	if err != nil {
		log.Warn().Str("tool", p.key).Err(err).Msg("tool plugin: unavailable")
		msg := fmt.Sprintf("Tool %q is starting or failed to start (%v). Try again in a moment.", p.key, err)
		if errors.Is(err, r.Context().Err()) && r.Context().Err() != nil {
			return
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	defer release()
	rp := &httputil.ReverseProxy{
		Transport:     rt,
		FlushInterval: -1, // SSE / streamed output goes through per write
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "plugin"
			pr.Out.Host = "plugin"
			injectHeaders(pr.Out.Header, p.base, req.user)
		},
		ModifyResponse: func(resp *http.Response) error { return p.modify(resp, r, req) },
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Warn().Str("tool", p.key).Err(err).Msg("tool plugin: proxy error")
			http.Error(w, fmt.Sprintf("Tool %q did not answer: %v", p.key, err), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// injectHeaders drops every client-sent X-Wick-* header, then sets the
// trusted ones from the host's own session.
func injectHeaders(h http.Header, base string, u *tool.User) {
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), wickplugin.HeaderPrefix) {
			delete(h, k)
		}
	}
	h.Set(wickplugin.HeaderBase, base)
	if u == nil {
		return
	}
	role := "user"
	if u.IsAdmin {
		role = "admin"
	}
	h.Set(wickplugin.HeaderUserID, u.ID)
	h.Set(wickplugin.HeaderUserEmail, u.Email)
	h.Set(wickplugin.HeaderUserName, u.Name)
	h.Set(wickplugin.HeaderUserRole, role)
	if len(u.Tags) > 0 {
		h.Set(wickplugin.HeaderUserTags, strings.Join(u.Tags, ","))
	}
}

func (p *Proxy) modify(resp *http.Response, r *http.Request, req request) error {
	if p.isStatic(r) && resp.StatusCode == http.StatusOK && resp.Header.Get("Cache-Control") == "" {
		resp.Header.Set("Cache-Control", "public, max-age=3600")
		resp.Header.Set("ETag", p.etag())
	}
	layout := resp.Header.Get(wickplugin.HeaderLayout)
	resp.Header.Del(wickplugin.HeaderLayout)
	resp.Header.Del(wickplugin.HeaderTitle)
	// HTMX swaps and JSON callers get the plugin's bytes untouched.
	if layout != wickplugin.LayoutPage || req.wrap == nil || r.Header.Get("HX-Request") == "true" {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	page := req.wrap(body)
	resp.Body = io.NopCloser(bytes.NewReader(page))
	resp.ContentLength = int64(len(page))
	resp.Header.Set("Content-Length", strconv.Itoa(len(page)))
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	return nil
}

// bufWriter captures wick's page render into memory.
type bufWriter struct {
	h    http.Header
	buf  bytes.Buffer
	code int
}

func (b *bufWriter) Header() http.Header         { return b.h }
func (b *bufWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufWriter) WriteHeader(code int)        { b.code = code }
