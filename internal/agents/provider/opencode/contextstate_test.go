package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The meter's scale and the auto-compaction flag are read once per server
// and model, not in front of every prompt.
func TestContextStateReadOncePerServer(t *testing.T) {
	var provider, config atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/provider":
			provider.Add(1)
			_, _ = w.Write([]byte(`{"all":[{"id":"opencode","models":{"mimo":{"id":"mimo","limit":{"context":200000}}}}]}`))
		case "/config":
			config.Add(1)
			_, _ = w.Write([]byte(`{"compaction":{"auto":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &apiClient{base: srv.URL, password: "pw", http: srv.Client()}
	for i := 0; i < 3; i++ {
		w, auto := contextState(context.Background(), c, "opencode/mimo")
		if w != 200000 || auto == nil || *auto {
			t.Fatalf("turn %d: window %d auto %v", i, w, auto)
		}
	}
	if provider.Load() != 1 || config.Load() != 1 {
		t.Fatalf("GET /provider ×%d, /config ×%d over 3 turns, want 1 each", provider.Load(), config.Load())
	}
}
