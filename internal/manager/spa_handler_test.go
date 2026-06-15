package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// withTestSPAFS swaps the package SPA filesystem for a synthetic bundle so
// the handler tests exercise the serve logic without depending on a real
// `npm run build` artifact — that bundle is gitignored and absent in CI
// (only dist/.gitkeep is committed), so reading the real embed would 404.
// Restored on cleanup.
func withTestSPAFS(t *testing.T) {
	t.Helper()
	prev := spaFS
	spaFS = fstest.MapFS{
		"dist/manager/index.html":           {Data: []byte(`<!doctype html><html><body><div id="app" data-base=""></div></body></html>`)},
		"dist/manager/assets/index-test.js": {Data: []byte("console.log(1);")},
	}
	t.Cleanup(func() { spaFS = prev })
}

func TestSPAHandlerServesShell(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, spaMount+"/", nil)
	rec := httptest.NewRecorder()
	h.spaHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html…", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-base="`+spaBase+`"`) {
		t.Errorf("served shell missing injected data-base=%q; body=%s", spaBase, body)
	}
}

func TestSPAHandlerInjectsDataBase(t *testing.T) {
	cases := []string{
		`<div id="app" data-base=""></div>`,
		`<div id="app" data-base="/wrong/base"></div>`,
		`<div id="app" data-base="/modules/manager/app"></div>`,
	}
	want := `data-base="` + spaBase + `"`
	for _, in := range cases {
		got := dataBaseRe.ReplaceAllString(in, want)
		if !strings.Contains(got, want) {
			t.Errorf("ReplaceAll(%q) = %q, want it to contain %q", in, got, want)
		}
		if strings.Contains(got, `data-base="/wrong/base"`) {
			t.Errorf("ReplaceAll left a stale base in %q", got)
		}
	}
}

func TestSPAHandlerClientRouteFallsBackToShell(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, spaMount+"/connectors/slack", nil)
	rec := httptest.NewRecorder()
	h.spaHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (shell fallback); body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html…", ct)
	}
}

func TestSPAHandlerServesAssetWithImmutableCache(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, spaMount+"/assets/index-test.js", nil)
	rec := httptest.NewRecorder()
	h.spaHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type = %q, want application/javascript…", ct)
	}
}
