package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/toolplugin"
)

func TestCount(t *testing.T) {
	if got := count("hello wick\nplugin"); got != (Counts{Chars: 17, Words: 3, Lines: 2}) {
		t.Fatalf("count = %+v", got)
	}
}

func TestSurfaces(t *testing.T) {
	h := toolplugin.Handler(module(), map[string]string{"webhook_secret": "s"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/example_counter", nil))
	if rec.Header().Get(wickplugin.HeaderLayout) != wickplugin.LayoutPage || !strings.Contains(rec.Body.String(), "<form") {
		t.Fatalf("page: %v %q", rec.Header(), rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/example_counter/api/count?text="+url.QueryEscape("a b"), nil))
	if strings.TrimSpace(rec.Body.String()) != `{"chars":3,"words":2,"lines":1}` {
		t.Fatalf("json: %q", rec.Body.String())
	}

	for secret, want := range map[string]int{"s": http.StatusOK, "x": http.StatusUnauthorized} {
		req := httptest.NewRequest("POST", "/tools/example_counter/webhook/count", strings.NewReader("one two"))
		req.Header.Set("X-Counter-Secret", secret)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("webhook secret %q: code %d", secret, rec.Code)
		}
	}

	tm := toolplugin.Manifest(module())
	if len(tm.Webhooks) != 1 || tm.Webhooks[0].Path != "/webhook/count" {
		t.Fatalf("manifest webhooks = %+v", tm.Webhooks)
	}
}
