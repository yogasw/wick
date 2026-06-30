package router9

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

// jsPathPrefixes are the root-absolute path prefixes 9router hard-codes
// as quoted string literals in its client bundles: fetch() targets
// (/api/...), Next asset URLs (/_next/...), and client-router
// navigation targets (/dashboard, /login, ...). Each is rewritten to
// sit under MountPrefix so the SPA works when proxied under a subpath.
//
// Order matters only for readability; matching is by exact quoted
// prefix so /login won't accidentally catch /logout etc. Add a prefix
// here if a new 9router route 404s through the proxy.
var jsPathPrefixes = []string{
	"/api/",
	"/_next/",
	"/dashboard",
	"/login",
	"/logout",
	"/onboarding",
	"/setup",
	"/providers",
	// Root-level static files 9router serves from "/".
	"/favicon.ico",
	"/favicon.svg",
	"/manifest.webmanifest",
	"/robots.txt",
}

// rewriteResponse adapts a root-absolute Next.js response so it works
// when the app is served under MountPrefix ("/9router"). 9router has no
// base-path flag, so it emits /login, /dashboard, /_next/, and JS-level
// fetch("/api/...") — all of which would otherwise resolve against the
// wick root. We:
//
//   - drop frame/CSP headers so the page can be embedded in our iframe,
//   - rewrite the Location header on redirects (/x -> /9router/x),
//   - for HTML, inject <base href="/9router/"> and prefix root-absolute
//     href/src/action URLs,
//   - for JS bundles, prefix the quoted absolute path literals 9router
//     uses for fetch() and client-side navigation.
//
// This is best-effort subpath-proxying of a SPA that has no base-path
// support: literal quoted paths are covered, but a path assembled from
// fragments at runtime could still slip through. If one does, add its
// prefix to jsPathPrefixes.
func rewriteResponse(r *http.Response) error {
	r.Header.Del("X-Frame-Options")
	r.Header.Del("Content-Security-Policy")
	r.Header.Del("Content-Security-Policy-Report-Only")

	// Redirect target: /dashboard -> /9router/dashboard.
	if loc := r.Header.Get("Location"); loc != "" {
		r.Header.Set("Location", prefixAbsolute(loc))
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
		out = rewriteHTML(string(body))
	case isCSS:
		out = rewriteCSS(string(body))
	default:
		out = rewriteJS(string(body))
	}

	r.Body = io.NopCloser(strings.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", itoa(len(out)))
	// We may have decoded gzip; serve plain so the length matches.
	r.Header.Del("Content-Encoding")
	// These bodies are rewritten on the fly; a cached copy would pin a
	// stale, pre-rewrite version (the exact bug where /api/... 404s after
	// a rewrite lands). Force revalidation. The SW also skips /9router/*.
	r.Header.Set("Cache-Control", "no-store, must-revalidate")
	return nil
}

// rewriteJS prefixes the quoted absolute path literals in a JS bundle.
// Only double-quoted, single-quoted, and backtick forms are handled —
// the three string delimiters webpack output uses. Idempotent: a path
// already under MountPrefix is left alone.
func rewriteJS(js string) string {
	for _, q := range []string{`"`, `'`, "`"} {
		for _, p := range jsPathPrefixes {
			from := q + p
			to := q + MountPrefix + p
			js = strings.ReplaceAll(js, from, to)
			// Undo any double-prefix from overlapping passes.
			js = strings.ReplaceAll(js, q+MountPrefix+MountPrefix+p, to)
		}
	}
	return js
}

// prefixAbsolute turns a root-absolute path into a MountPrefix-rooted
// one. Leaves protocol-relative (//host) and absolute (http://) URLs and
// already-prefixed paths alone.
func prefixAbsolute(u string) string {
	if u == "" || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return u
	}
	if u == MountPrefix || strings.HasPrefix(u, MountPrefix+"/") {
		return u
	}
	return MountPrefix + u
}

// htmlDelims are the byte sequences that, in 9router's SSR HTML, precede
// a root-absolute path that must be prefixed. Next.js emits paths inside
// HTML attributes (="/...), in inlined JSON arrays/strings ("/...), and
// inside the RSC flight stream where the quotes are escaped (\"/...). We
// rewrite each delimiter+prefix pair.
var htmlDelims = []string{`="`, `"`, `\"`, `'`, "`", `,"`, `["`}

// rewriteHTML injects a <base> tag and prefixes the root-absolute paths
// 9router hard-codes in its SSR HTML and RSC flight stream (assets under
// /_next/, navigation targets, and fetch endpoints). It keys off the
// path prefixes in jsPathPrefixes so HTML and JS rewriting stay in sync.
func rewriteHTML(html string) string {
	// Inject <base> right after <head> so relative URLs resolve under
	// the prefix. Only once.
	if i := indexFold(html, "<head>"); i >= 0 {
		at := i + len("<head>")
		html = html[:at] + `<base href="` + MountPrefix + `/">` + html[at:]
	}
	for _, d := range htmlDelims {
		for _, p := range jsPathPrefixes {
			from := d + p
			to := d + MountPrefix + p
			html = strings.ReplaceAll(html, from, to)
			html = strings.ReplaceAll(html, d+MountPrefix+MountPrefix+p, to)
		}
	}
	return html
}

// rewriteCSS prefixes root-absolute url() references in a stylesheet.
// 9router's CSS embeds fonts/images as url(/_next/static/media/...),
// which 404 under the subpath unless prefixed. Handles the three url()
// quoting styles: url(/..), url('/..'), url("/..").
func rewriteCSS(css string) string {
	for _, open := range []string{"url(/", "url('/", `url("/`} {
		to := open[:len(open)-1] + MountPrefix + "/"
		css = strings.ReplaceAll(css, open, to)
		// Undo any double-prefix.
		css = strings.ReplaceAll(css, open[:len(open)-1]+MountPrefix+MountPrefix+"/", to)
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
