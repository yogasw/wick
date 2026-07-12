package airouter

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// rewriter adapts a router's root-absolute Next.js responses so the SPA works
// when proxied under a per-router subpath (prefix, e.g. "/airouter/9router").
// One rewriter per Manager, capturing its prefix + id.
type rewriter struct {
	prefix string
	id     string
	// prefixes is the full set of root-absolute path prefixes to re-root under
	// prefix — baseRewritePrefixes plus this router's Descriptor.RoutePrefixes.
	prefixes []string
}

// baseRewritePrefixes are the root-absolute path prefixes common to the
// embedded router SPAs (Next.js apps): fetch() targets (/api/…), Next asset
// URLs (/_next/…), auth/OAuth, and the usual dashboard routes. Each is
// rewritten to sit under the per-router prefix so the SPA works under a
// subpath. Router-specific top-level routes (e.g. OmniRoute's /home) are added
// per router via Descriptor.RoutePrefixes so one app's routes don't leak into
// another's rewrite pass.
//
// /_next/ IS rewritten here: with two dashboards running concurrently a shared
// root /_next/ can't disambiguate which backend an asset belongs to, so we
// namespace static references under the router prefix. Runtime-assembled
// /_next/ URLs that still slip through to the root are caught by the
// active-asset-router fallback (see NextAssetProxy).
var baseRewritePrefixes = []string{
	"/api/",
	"/_next/",
	"/dashboard",
	"/login",
	"/logout",
	"/onboarding",
	"/setup",
	"/providers",
	"/endpoints",
	"/callback",
	"/oauth/",
	"/favicon.ico",
	"/favicon.svg",
	"/manifest.webmanifest",
	"/robots.txt",
}

// rewritePrefixesFor returns the merged prefix list for a router: the common
// base plus its declared app routes. Router routes come first so a more
// specific match wins, though matching is by exact quoted/delimited prefix so
// order only affects readability.
func rewritePrefixesFor(routePrefixes []string) []string {
	out := make([]string, 0, len(baseRewritePrefixes)+len(routePrefixes))
	out = append(out, baseRewritePrefixes...)
	out = append(out, routePrefixes...)
	return out
}

// activeAssetRouter holds the ID of the router whose dashboard HTML was last
// served, so root-absolute /_next/* requests assembled at runtime (which carry
// no router context) can be routed to the right backend. Admin-only tool, one
// dashboard viewed at a time even while both processes run.
var activeAssetRouter atomic.Value // string

func markAssetRouter(id string) {
	if id != "" {
		activeAssetRouter.Store(id)
	}
}

// ActiveAssetRouter returns the router ID that should serve a bare /_next/*
// asset request, or "" when none has been resolved yet.
func ActiveAssetRouter() string {
	if v, ok := activeAssetRouter.Load().(string); ok {
		return v
	}
	return ""
}

// rewriteResponse adapts a root-absolute Next.js response so it works under
// the router prefix: drops frame/CSP headers for iframe embedding, rewrites
// the Location header, injects <base> + prefixes root-absolute paths in HTML,
// and prefixes the quoted absolute path literals in JS/CSS.
func (rw rewriter) rewriteResponse(r *http.Response) error {
	r.Header.Del("X-Frame-Options")
	r.Header.Del("Content-Security-Policy")
	r.Header.Del("Content-Security-Policy-Report-Only")

	if loc := r.Header.Get("Location"); loc != "" {
		r.Header.Set("Location", rw.prefixAbsolute(loc))
	}

	ct := strings.ToLower(r.Header.Get("Content-Type"))
	isHTML := strings.Contains(ct, "text/html")
	isJS := strings.Contains(ct, "javascript")
	isCSS := strings.Contains(ct, "text/css")
	if !isHTML && !isJS && !isCSS {
		return nil
	}

	body, err := readBody(r)
	if err != nil {
		return err
	}
	var out string
	switch {
	case isHTML:
		// Serving the dashboard HTML makes this router the asset source for
		// subsequent bare /_next/* requests assembled at runtime.
		markAssetRouter(rw.id)
		out = rw.rewriteHTML(string(body))
	case isCSS:
		out = rw.rewriteCSS(string(body))
	default:
		out = rw.rewriteJS(string(body))
	}

	r.Body = io.NopCloser(strings.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", itoa(len(out)))
	r.Header.Del("Content-Encoding")
	r.Header.Set("Cache-Control", "no-store, must-revalidate")
	return nil
}

func (rw rewriter) rewriteJS(js string) string {
	for _, q := range []string{`"`, `'`, "`", "}"} {
		for _, p := range rw.prefixes {
			from := q + p
			to := q + rw.prefix + p
			js = strings.ReplaceAll(js, from, to)
			js = strings.ReplaceAll(js, q+rw.prefix+rw.prefix+p, to)
		}
	}
	return js
}

// prefixAbsolute turns a root-absolute path into a prefix-rooted one. Leaves
// protocol-relative and absolute URLs and already-prefixed paths alone.
func (rw rewriter) prefixAbsolute(u string) string {
	if u == "" || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return u
	}
	if u == rw.prefix || strings.HasPrefix(u, rw.prefix+"/") {
		return u
	}
	return rw.prefix + u
}

var htmlDelims = []string{`="`, `"`, `\"`, `'`, "`", `,"`, `["`}

func (rw rewriter) rewriteHTML(html string) string {
	if i := indexFold(html, "<head>"); i >= 0 {
		at := i + len("<head>")
		html = html[:at] + `<base href="` + rw.prefix + `/">` + html[at:]
	}
	for _, d := range htmlDelims {
		for _, p := range rw.prefixes {
			from := d + p
			to := d + rw.prefix + p
			html = strings.ReplaceAll(html, from, to)
			html = strings.ReplaceAll(html, d+rw.prefix+rw.prefix+p, to)
		}
	}
	return html
}

func (rw rewriter) rewriteCSS(css string) string {
	for _, open := range []string{"url(/", "url('/", `url("/`} {
		to := open[:len(open)-1] + rw.prefix + "/"
		css = strings.ReplaceAll(css, open, to)
		css = strings.ReplaceAll(css, open[:len(open)-1]+rw.prefix+rw.prefix+"/", to)
	}
	return css
}

func readBody(r *http.Response) ([]byte, error) {
	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}
	defer r.Body.Close()
	return io.ReadAll(reader)
}

// indexFold is a case-insensitive bytes.Index for ASCII needles.
func indexFold(s, sub string) int {
	return bytes.Index(bytes.ToLower([]byte(s)), []byte(strings.ToLower(sub)))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
