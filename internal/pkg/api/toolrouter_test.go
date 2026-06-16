package api

import (
	"net/http"
	"net/http/httptest"
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
