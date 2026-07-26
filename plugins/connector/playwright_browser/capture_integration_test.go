//go:build browser_integration

// Integration tests for record_request that drive a REAL headless Chromium.
//
// Run with:  go test -tags browser_integration ./plugins/connector/playwright_browser/ -run Integration
//
// Gated behind the browser_integration build tag (and skipped under -short)
// because they launch a browser and download it on first use — too heavy for
// the default unit run / CI. They validate the end-to-end capture path the pure
// unit tests can't: that the live OnRequestFinished listener actually fires,
// dedups repeated fetches, and records cross-domain calls.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// runOp drives the `run` op through a fresh ephemeral browser with the given
// input, returning the parsed step result. record_* inputs are honored.
// session_dir is a CONFIG value (sessionDir reads c.Cfg, not c.Input), so it's
// lifted out of the input map into the config here — where capture files land.
func runOp(t *testing.T, input map[string]string) map[string]any {
	t.Helper()
	if testing.Short() {
		t.Skip("browser integration test skipped in -short")
	}
	cfg := map[string]string{"browser": "chromium", "headless": "true"}
	if sd := input["session_dir"]; sd != "" {
		cfg["session_dir"] = sd
		delete(input, "session_dir")
	}
	c := connector.NewCtx(context.Background(), "test-instance", cfg, input, nil, nil, nil)
	out, err := run(c)
	if err != nil {
		t.Fatalf("run op failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("run returned %T, want map", out)
	}
	return m
}

// pageThatFetches serves an HTML page whose script fetches each url the given
// number of times — used to provoke same-domain dedup and cross-domain capture.
func pageThatFetches(urls map[string]int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			// Any non-root path is an "API" endpoint the page fetches.
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		var js string
		for u, n := range urls {
			for i := 0; i < n; i++ {
				js += fmt.Sprintf("await fetch(%q);\n", u)
			}
		}
		w.Header().Set("content-type", "text/html")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><body><script>
(async () => { %s })();
</script></body></html>`, js)
	}
}

// readCapture loads the saved capture for the given record_name.
func readCapture(t *testing.T, c *connector.Ctx) []CapturedRequest {
	t.Helper()
	path, err := captureSavePath(c, "", "itest")
	if err != nil {
		t.Fatalf("captureSavePath: %v", err)
	}
	reqs, err := loadCapture(path)
	if err != nil {
		t.Fatalf("loadCapture: %v", err)
	}
	return reqs
}

// TestIntegrationRecordDedupSameDomain: a page that fetches the SAME endpoint 5
// times records it once (dedup), while a distinct endpoint on the same domain is
// its own entry.
func TestIntegrationRecordDedupSameDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test skipped in -short")
	}
	srv := httptest.NewServer(nil)
	defer srv.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/", pageThatFetches(map[string]int{
		srv.URL + "/api/poll": 5, // same endpoint 5x → dedup to 1
		srv.URL + "/api/once": 1,
	}))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv.Config.Handler = mux

	tmp := t.TempDir()
	input := map[string]string{
		"session_dir":           tmp,
		"record_request":        "true",
		"record_name":           "itest",
		"record_include_assets": "false",
		"actions":               fmt.Sprintf(`[{"action":"goto","url":%q},{"action":"wait","ms":1500}]`, srv.URL+"/"),
	}
	runOp(t, input)

	c := connector.NewCtx(context.Background(), "test-instance", map[string]string{"session_dir": tmp}, map[string]string{}, nil, nil, nil)
	got := readCapture(t, c)

	poll, once := 0, 0
	for _, r := range got {
		switch r.URL {
		case srv.URL + "/api/poll":
			poll++
			// The response side must now be filled (status 200), not left 0 —
			// this is the regression the OnResponse listener fixes.
			if r.Status != 200 {
				t.Errorf("expected response status 200 for /api/poll, got %d", r.Status)
			}
		case srv.URL + "/api/once":
			once++
		}
	}
	if poll != 1 {
		t.Errorf("same endpoint fetched 5x should dedup to 1, got %d", poll)
	}
	if once != 1 {
		t.Errorf("distinct endpoint should record 1, got %d", once)
	}
}

// TestIntegrationRecordCrossDomain: a page that fetches endpoints on TWO
// different origins records both (cross-domain is kept).
func TestIntegrationRecordCrossDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test skipped in -short")
	}
	// Second "domain" (different port = different origin).
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("access-control-allow-origin", "*")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer other.Close()

	srv := httptest.NewServer(nil)
	defer srv.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/", pageThatFetches(map[string]int{
		srv.URL + "/api/local":   1,
		other.URL + "/api/remote": 1,
	}))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv.Config.Handler = mux

	tmp := t.TempDir()
	input := map[string]string{
		"session_dir":    tmp,
		"record_request": "true",
		"record_name":    "itest",
		"actions":        fmt.Sprintf(`[{"action":"goto","url":%q},{"action":"wait","ms":1500}]`, srv.URL+"/"),
	}
	runOp(t, input)

	c := connector.NewCtx(context.Background(), "test-instance", map[string]string{"session_dir": tmp}, map[string]string{}, nil, nil, nil)
	got := readCapture(t, c)

	var haveLocal, haveRemote bool
	for _, r := range got {
		if r.URL == srv.URL+"/api/local" {
			haveLocal = true
		}
		if r.URL == other.URL+"/api/remote" {
			haveRemote = true
		}
	}
	if !haveLocal || !haveRemote {
		b, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("cross-domain: local=%v remote=%v; capture=%s", haveLocal, haveRemote, b)
	}
}
