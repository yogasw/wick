package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPInflight locks what the drain counts as an in-flight request: a
// normal request is work to wait for, a stream is not (it never ends on its
// own, so waiting for one would mean never handing over).
func TestHTTPInflight(t *testing.T) {
	t.Run("a request in a handler is counted", func(t *testing.T) {
		h := newHTTPInflight()
		var during int
		var names []string
		srv := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			during = h.Count()
			names = h.Names()
			w.WriteHeader(http.StatusOK)
		}))
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/things", nil))
		if during != 1 {
			t.Fatalf("in-flight during handler = %d, want 1", during)
		}
		if len(names) != 1 || names[0] != "POST /api/things" {
			t.Fatalf("names = %v", names)
		}
		if after := h.Count(); after != 0 {
			t.Fatalf("in-flight after handler = %d, want 0", after)
		}
	})

	t.Run("an SSE response stops counting as soon as it says so", func(t *testing.T) {
		h := newHTTPInflight()
		var afterHeader int
		srv := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// From here the handler may sit for hours; the drain must not.
			afterHeader = h.Count()
		}))
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/stream", nil))
		if afterHeader != 0 {
			t.Fatalf("stream still counted after its header = %d, want 0", afterHeader)
		}
	})

	t.Run("observability endpoints never count", func(t *testing.T) {
		// A status poll that counts itself would keep the process "busy" for
		// as long as somebody watches the page.
		h := newHTTPInflight()
		for _, path := range []string{"/health", "/boot-status", "/admin/advanced/software-update/serving"} {
			var during int
			srv := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				during = h.Count()
			}))
			srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
			if during != 0 {
				t.Fatalf("%s counted as work", path)
			}
		}
	})
}
