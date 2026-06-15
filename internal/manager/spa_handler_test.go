package manager

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAHandlerServesShell(t *testing.T) {
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
	asset := firstBuiltJSAsset(t)
	if asset == "" {
		t.Skip("no built JS asset to probe (run npm build first)")
	}

	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, spaMount+"/assets/"+asset, nil)
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

// firstBuiltJSAsset returns the name of the first built JS asset under
// dist/manager/assets, or "" when the bundle has not been built.
func firstBuiltJSAsset(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(spaFS, "dist/manager/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			return e.Name()
		}
	}
	return ""
}
