package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/yogasw/wick/internal/pkg/ui"
)

// withTestSPAFS swaps the package SPA filesystem for a synthetic bundle so
// the handler tests exercise the serve logic without depending on a real
// `npm run build` artifact — that bundle is gitignored and absent in CI
// (only dist/.gitkeep is committed), so reading the real embed would 404.
// Restored on cleanup. The asset resolver caches its result once per
// process, so the synthetic index.html must declare the bundle src the
// asset test expects.
func withTestSPAFS(t *testing.T) {
	t.Helper()
	prev := spaFS
	spaFS = fstest.MapFS{
		"dist/manager/index.html": {Data: []byte(
			`<!doctype html><html lang="en"><head>` +
				`<script type="module" crossorigin src="/manager/_app/assets/index-test.js"></script>` +
				`</head><body><div id="app" data-base=""></div></body></html>`)},
		"dist/manager/assets/index-test.js": {Data: []byte("console.log(1);")},
	}
	t.Cleanup(func() {
		spaFS = prev
		assetURLOnce = sync.Once{}
		assetURL = ""
	})
	assetURLOnce = sync.Once{}
	assetURL = ""
}

func TestServeSPAShellRendersHostChrome(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/manager", nil)
	rec := httptest.NewRecorder()
	h.serveSPAShell(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html…", ct)
	}
	body := rec.Body.String()
	checks := []string{
		`data-base="` + spaBase + `"`,        // SPA route prefix injected
		`<title>Manager`,                     // host ui.Layout title
		`/manager/_app/assets/index-test.js`, // hashed bundle from asset base
		`<script type="module"`,              // module script tag present
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("served shell missing %q; body=%s", want, body)
		}
	}
}

func TestServeSPAShellInheritsThemeFromContext(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/manager", nil)
	req = req.WithContext(ui.WithTheme(req.Context(), "dark"))
	rec := httptest.NewRecorder()
	h.serveSPAShell(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `class="theme-dark dark"`) {
		t.Errorf("served shell missing dark theme class from host Layout; body=%s", body)
	}
}

func TestServeSPAShellFallbackWhenBundleMissing(t *testing.T) {
	prev := spaFS
	spaFS = fstest.MapFS{} // no dist/manager → empty asset URL
	assetURLOnce = sync.Once{}
	assetURL = ""
	t.Cleanup(func() {
		spaFS = prev
		assetURLOnce = sync.Once{}
		assetURL = ""
	})
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, "/manager", nil)
	rec := httptest.NewRecorder()
	h.serveSPAShell(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bundle not built yet") {
		t.Errorf("expected not-built fallback message; body=%s", rec.Body.String())
	}
}

func TestSPAAssetHandlerServesWithImmutableCache(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, spaAssetBase+"assets/index-test.js", nil)
	rec := httptest.NewRecorder()
	h.spaAssetHandler(rec, req)

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

func TestSPAAssetHandlerRejectsNonAssetPath(t *testing.T) {
	withTestSPAFS(t)
	h := &Handler{}

	req := httptest.NewRequest(http.MethodGet, spaAssetBase+"index.html", nil)
	rec := httptest.NewRecorder()
	h.spaAssetHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for non-asset path; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSPAAssetURLReadsHashedBundle(t *testing.T) {
	withTestSPAFS(t)
	if got := spaAssetURL(); got != "/manager/_app/assets/index-test.js" {
		t.Errorf("spaAssetURL() = %q, want /manager/_app/assets/index-test.js", got)
	}
}
