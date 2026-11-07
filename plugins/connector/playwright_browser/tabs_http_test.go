package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTabsOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"type":"page","url":"https://example.com/a","title":"A"},
			{"type":"iframe","url":"https://example.com/frame","title":"F"},
			{"type":"page","url":"about:blank","title":""}
		]`))
	}))
	defer srv.Close()
	tabs := tabsOverHTTP(srv.URL)
	if len(tabs) != 2 {
		t.Fatalf("want 2 pages, got %v", tabs)
	}
	if tabs[0]["url"] != "https://example.com/a" || tabs[1]["index"] != 1 {
		t.Fatalf("unexpected tabs %v", tabs)
	}
	if tabsOverHTTP("http://127.0.0.1:1") != nil {
		t.Fatal("an unreachable browser lists no tabs")
	}
}
