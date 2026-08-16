package agents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The SPA asset handler is mounted at spaPrefix only, so every app's Vite
// `base` must live under it. A base outside that prefix builds and deploys
// fine and then 404s in production — which is exactly what happened with
// this page's first build.
//
// This drives the handler the way the browser does: the path arrives
// already stripped of the tool mount.
//
// Skips when dist/resources/ is absent, matching every other SPA test
// here (spa_handler_test.go, spa_integration_test.go). Go tests run
// BEFORE `npm run build` in the release pipeline, so an unbuilt bundle is
// a normal state for this suite, not a failure — asserting on it would
// make the Go job depend on a frontend build it never runs.
func TestResourcesSPAAssetsAreServed(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/resources/", nil)

	spaHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Skipf("no resources SPA shell (%d) — run `npm run build` in fe/ to enable: %s",
			rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The shell must point at an asset path this same handler serves.
	body := rec.Body.String()
	const wantBase = "/tools/agents/workflow/resources/assets/"
	if !strings.Contains(body, wantBase) {
		t.Fatalf("shell does not reference %q — its Vite base is outside the SPA mount:\n%s",
			wantBase, body)
	}
}

// Follow the reference the shell actually emits, so a mismatch between the
// baked base and the served route cannot pass.
func TestResourcesSPABundleResolves(t *testing.T) {
	shell := httptest.NewRecorder()
	spaHandler(shell, httptest.NewRequest(http.MethodGet, "/resources/", nil))
	if shell.Code != http.StatusOK {
		t.Skipf("resources bundle not built (%d); nothing to resolve", shell.Code)
	}

	// Pull the src="..." the browser would request.
	body := shell.Body.String()
	i := strings.Index(body, `src="/tools/agents/workflow/resources/assets/`)
	if i < 0 {
		t.Fatalf("no asset reference in shell:\n%s", body)
	}
	rest := body[i+len(`src="`):]
	url := rest[:strings.Index(rest, `"`)]

	// Strip the tool mount + SPA prefix the way HandleRaw does.
	inner := strings.TrimPrefix(url, "/tools/agents/workflow")

	rec := httptest.NewRecorder()
	spaHandler(rec, httptest.NewRequest(http.MethodGet, inner, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("asset %s = %d, want 200 — the shell points somewhere the handler does not serve",
			url, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("asset Content-Type = %q, want a JavaScript type", ct)
	}
}
