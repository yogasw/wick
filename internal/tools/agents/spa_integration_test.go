package agents

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/yogasw/wick/pkg/tool"
)

// testRouter is a minimal tool.Router implementation backed by
// http.ServeMux. It mounts what registerSPA registers and nothing else
// — keeps the test focused on the SPA contract without booting the
// whole agents handler graph (which needs a DB, providers, channels).
type testRouter struct {
	mux  *http.ServeMux
	meta tool.Tool
}

func newTestRouter() *testRouter {
	return &testRouter{
		mux:  http.NewServeMux(),
		meta: tool.Tool{Key: "agents", Path: "/tools/agents"},
	}
}

func (t *testRouter) base(p string) string {
	return path.Join(t.meta.Path, p)
}

func (t *testRouter) Meta() tool.Tool { return t.meta }

func (t *testRouter) Static(prefix string, fsys fs.FS) {
	full := t.base(prefix)
	if !strings.HasSuffix(full, "/") {
		full += "/"
	}
	t.mux.Handle(full, tool.StaticHandler(full, fsys))
}

func (t *testRouter) GET(p string, h tool.HandlerFunc)  { t.handle("GET", p, h) }
func (t *testRouter) POST(p string, h tool.HandlerFunc) { t.handle("POST", p, h) }
func (t *testRouter) PUT(p string, h tool.HandlerFunc)  { t.handle("PUT", p, h) }
func (t *testRouter) DELETE(p string, h tool.HandlerFunc) {
	t.handle("DELETE", p, h)
}
func (t *testRouter) PATCH(p string, h tool.HandlerFunc) {
	t.handle("PATCH", p, h)
}
func (t *testRouter) HandleRaw(prefix string, fn func(cfg tool.ConfigReader) http.Handler) {
	// Mirror the real toolRouter — mount the handler as-is, no
	// StripPrefix. The caller is expected to strip its own mount path
	// (see registerSPA for the pattern).
	full := t.base(prefix)
	if !strings.HasSuffix(full, "/") {
		full += "/"
	}
	t.mux.Handle(full, fn(nil))
}

func (t *testRouter) handle(method, p string, h tool.HandlerFunc) {
	pattern := method + " " + t.base(p)
	render := func(c *tool.Ctx, body templ.Component) {
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = body.Render(c.R.Context(), c.W)
	}
	t.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		ctx := tool.NewCtx(w, r, render, t.meta, nil)
		h(ctx)
	})
}

// skipIfNoSPAShell skips when the Vite bundle isn't built. index.html +
// assets are not committed (regenerated at build/release time, mirror of
// *_templ.go), so any test that hits the SPA HTTP surface needs to opt
// out gracefully on fresh clones / CI without a build step.
func skipIfNoSPAShell(t *testing.T) {
	t.Helper()
	sub, err := fs.Sub(SPAFS, "dist/workflow")
	if err != nil {
		t.Skip("no SPA dist subtree — run `npm run build:workflow` to enable")
	}
	if _, err := sub.Open("index.html"); err != nil {
		t.Skip("no SPA shell — run `npm run build:workflow` to enable")
	}
}

// TestSPAShellServes ensures hitting /tools/agents/agents-v2/workflow/
// returns the Vite-built index.html with the right base URL injected.
func TestSPAShellServes(t *testing.T) {
	skipIfNoSPAShell(t)
	r := newTestRouter()
	registerSPA(r)

	srv := httptest.NewServer(r.mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/tools/agents/agents-v2/workflow/")
	if err != nil {
		t.Fatalf("GET shell: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: got %q want text/html", ct)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "/tools/agents/agents-v2/workflow/") {
		t.Errorf("shell missing SPA base URL; body: %s", string(body))
	}
}

// TestSPAShellClientRoute ensures a /edit/<id> hash-route fallback
// returns the same shell — the SPA owns routing client-side.
func TestSPAShellClientRoute(t *testing.T) {
	skipIfNoSPAShell(t)
	r := newTestRouter()
	registerSPA(r)
	srv := httptest.NewServer(r.mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/tools/agents/agents-v2/workflow/edit/abc-123")
	if err != nil {
		t.Fatalf("GET client route: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "/tools/agents/agents-v2/workflow/") {
		t.Errorf("client route shell missing base URL; body: %s", string(body))
	}
}

// TestSPAAssetServes hits an actual built JS bundle through the Static
// mount. Verifies asset URLs in index.html are reachable.
func TestSPAAssetServes(t *testing.T) {
	r := newTestRouter()
	registerSPA(r)
	srv := httptest.NewServer(r.mux)
	defer srv.Close()

	// Pick the first .js file from the embed.
	sub, err := fs.Sub(SPAFS, "dist/workflow/assets")
	if err != nil {
		t.Skip("no assets dir")
	}
	entries, _ := fs.ReadDir(sub, ".")
	var jsName string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			jsName = e.Name()
			break
		}
	}
	if jsName == "" {
		t.Skip("no .js asset")
	}

	res, err := http.Get(srv.URL + "/tools/agents/agents-v2/workflow/assets/" + jsName)
	if err != nil {
		t.Fatalf("GET asset: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("asset status: got %d want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if len(body) < 100 {
		t.Errorf("asset body too small: %d bytes", len(body))
	}
}

// TestSPABareRedirect ensures hitting the bare /agents-v2/ root lands
// on the workflow app.
func TestSPABareRedirect(t *testing.T) {
	r := newTestRouter()
	registerSPA(r)
	srv := httptest.NewServer(r.mux)
	defer srv.Close()

	c := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := c.Get(srv.URL + "/tools/agents/agents-v2/")
	if err != nil {
		t.Fatalf("GET root: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("status: got %d want 302", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	// Relative redirect — browser resolves against the current path
	// (/tools/agents/agents-v2/), so "workflow/" lands at the right
	// SPA root. The absolute-URL path is hidden by HandleRaw's
	// StripPrefix so the handler emits a relative target.
	if !strings.HasSuffix(loc, "/workflow/") {
		t.Errorf("redirect target: got %q want suffix /workflow/", loc)
	}
}
