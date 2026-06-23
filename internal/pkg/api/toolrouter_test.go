package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// TestRootRouteTrailingSlashAllMethods is a regression test for the bug
// where a tool's root route was reachable with a trailing slash only for
// GET, so a POST/DELETE to "/tools/{key}/" returned 405 (it matched the
// GET-only {$} pattern). The new-session SPA POSTs to "${base}/" to start
// a session, which surfaced this as a 405 on send.
func TestRootRouteTrailingSlashAllMethods(t *testing.T) {
	r := newToolRouter(nil)
	r.withScope(tool.Tool{Key: "demo"}, false, func(rr tool.Router) {
		rr.GET("/", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusOK) })
		rr.POST("/", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusOK) })
	})
	mux := http.NewServeMux()
	r.mount(mux)

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/tools/demo"},
		{"GET", "/tools/demo/"},
		{"POST", "/tools/demo"},
		{"POST", "/tools/demo/"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s = %d, want 200", tc.method, tc.path, rec.Code)
			}
		})
	}
}

func TestMwCovers(t *testing.T) {
	cases := []struct {
		prefix, path string
		want         bool
	}{
		{"/tools/a/sessions/{id}", "/tools/a/sessions/{id}", true},        // exact
		{"/tools/a/sessions/{id}", "/tools/a/sessions/{id}/send", true},   // nested
		{"/tools/a/sessions/{id}", "/tools/a/sessions/{id}/git/push", true}, // deep nested
		{"/tools/a/sessions/{id}", "/tools/a/sessions", false},            // shorter / parent
		{"/tools/a/sessions/{id}", "/tools/a/sessions/{id}x", false},      // sibling, not a boundary
		{"/tools/a/sessions/{id}", "/tools/a/api/sessions/{id}", false},   // different subtree
		{"", "/tools/a/sessions/{id}", false},                             // empty prefix never matches
	}
	for _, tc := range cases {
		if got := mwCovers(tc.prefix, tc.path); got != tc.want {
			t.Errorf("mwCovers(%q, %q) = %v, want %v", tc.prefix, tc.path, got, tc.want)
		}
	}
}

// TestUseMiddlewareWiring verifies Use wraps only covered routes, runs
// outermost-first, and can short-circuit before the handler.
func TestUseMiddlewareWiring(t *testing.T) {
	var trail []string
	r := newToolRouter(nil)
	r.withScope(tool.Tool{Key: "demo"}, false, func(rr tool.Router) {
		// Order tag — first registered must be outermost.
		rr.Use("/sessions/{id}", func(next tool.HandlerFunc) tool.HandlerFunc {
			return func(c *tool.Ctx) { trail = append(trail, "mw1"); next(c) }
		})
		rr.Use("/sessions/{id}", func(next tool.HandlerFunc) tool.HandlerFunc {
			return func(c *tool.Ctx) { trail = append(trail, "mw2"); next(c) }
		})
		// Gate that short-circuits when ?deny=1.
		rr.Use("/sessions/{id}", func(next tool.HandlerFunc) tool.HandlerFunc {
			return func(c *tool.Ctx) {
				if c.R.URL.Query().Get("deny") == "1" {
					c.W.WriteHeader(http.StatusForbidden)
					return
				}
				next(c)
			}
		})
		rr.GET("/sessions/{id}/send", func(c *tool.Ctx) {
			trail = append(trail, "handler")
			c.W.WriteHeader(http.StatusOK)
		})
		// Sibling route NOT covered by the prefix.
		rr.GET("/sessions", func(c *tool.Ctx) {
			trail = append(trail, "list")
			c.W.WriteHeader(http.StatusOK)
		})
	})
	mux := http.NewServeMux()
	r.mount(mux)

	// Covered route, allowed → all middlewares run outermost-first, then handler.
	trail = nil
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/demo/sessions/abc/send", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("covered allowed: code %d want 200", rec.Code)
	}
	if got := strings.Join(trail, ","); got != "mw1,mw2,handler" {
		t.Fatalf("covered order = %q want mw1,mw2,handler", got)
	}

	// Covered route, denied → gate short-circuits, handler never runs.
	trail = nil
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/demo/sessions/abc/send?deny=1", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied: code %d want 403", rec.Code)
	}
	if strings.Contains(strings.Join(trail, ","), "handler") {
		t.Fatalf("denied: handler ran, trail=%v", trail)
	}

	// Uncovered sibling → no middleware runs.
	trail = nil
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/demo/sessions", nil))
	if got := strings.Join(trail, ","); got != "list" {
		t.Fatalf("uncovered route ran middleware: trail=%q", got)
	}
}
