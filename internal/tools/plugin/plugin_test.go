package plugin

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-h/templ"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

type fakeConn struct {
	mu  sync.Mutex
	cfg []map[string]string
}

func (f *fakeConn) Configure(_ context.Context, cfg map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = append(f.cfg, cfg)
	return nil
}
func (f *fakeConn) Health(context.Context) error { return nil }

// plugin is a fake tool plugin: it serves h on the socket like ServeTool.
type plugin struct {
	h     http.Handler
	delay time.Duration
	conn  fakeConn
	n     atomic.Int64
}

func (p *plugin) spawn(_, socket string) (func(), wickplugin.ToolConn, func() bool, error) {
	p.n.Add(1)
	time.Sleep(p.delay)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, nil, nil, err
	}
	srv := &http.Server{Handler: p.h}
	go srv.Serve(ln)
	var dead atomic.Bool
	return func() { dead.Store(true); srv.Close() }, &p.conn, func() bool { return !dead.Load() }, nil
}

func echoHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/tools/demo/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/page") {
			w.Header().Set(wickplugin.HeaderLayout, wickplugin.LayoutPage)
			w.Header().Set(wickplugin.HeaderTitle, "Demo")
			io.WriteString(w, "<p>frag</p>")
			return
		}
		if strings.Contains(r.URL.Path, "/static/") {
			io.WriteString(w, "body{}")
			return
		}
		hdr := map[string]string{}
		for k := range r.Header {
			if strings.HasPrefix(k, "X-Wick-") {
				hdr[k] = r.Header.Get(k)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hdr)
	})
	return mux
}

func sockDir(t *testing.T) string {
	// unix socket paths are length-limited; keep them short.
	d, err := os.MkdirTemp("", "tp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// recRouter records what a module registers.
type recRouter struct {
	routes []string
	hooks  []string
	groups []string
	h      map[string]tool.HandlerFunc
	wh     map[string]tool.WebhookHandlerFunc
}

func (r *recRouter) add(m, p string, h tool.HandlerFunc) {
	r.routes = append(r.routes, m+" "+p)
	r.h[m+" "+p] = h
}
func (r *recRouter) GET(p string, h tool.HandlerFunc)                       { r.add("GET", p, h) }
func (r *recRouter) POST(p string, h tool.HandlerFunc)                      { r.add("POST", p, h) }
func (r *recRouter) PUT(p string, h tool.HandlerFunc)                       { r.add("PUT", p, h) }
func (r *recRouter) DELETE(p string, h tool.HandlerFunc)                    { r.add("DELETE", p, h) }
func (r *recRouter) PATCH(p string, h tool.HandlerFunc)                     { r.add("PATCH", p, h) }
func (r *recRouter) Use(string, tool.Middleware)                            {}
func (r *recRouter) Static(string, fs.FS)                                   {}
func (r *recRouter) Meta() tool.Tool                                        { return tool.Tool{} }
func (r *recRouter) HandleRaw(string, func(tool.ConfigReader) http.Handler) {}
func (r *recRouter) WebhookGroup(g string) tool.WebhookRouter {
	r.groups = append(r.groups, g)
	return &recHook{r: r, g: g}
}

type recHook struct {
	r *recRouter
	g string
}

func (h *recHook) add(m, p string, f tool.WebhookHandlerFunc) {
	h.r.hooks = append(h.r.hooks, m+" "+h.g+p)
	h.r.wh[m+" "+h.g+p] = f
}
func (h *recHook) GET(p string, f tool.WebhookHandlerFunc)    { h.add("GET", p, f) }
func (h *recHook) POST(p string, f tool.WebhookHandlerFunc)   { h.add("POST", p, f) }
func (h *recHook) PUT(p string, f tool.WebhookHandlerFunc)    { h.add("PUT", p, f) }
func (h *recHook) DELETE(p string, f tool.WebhookHandlerFunc) { h.add("DELETE", p, f) }
func (h *recHook) PATCH(p string, f tool.WebhookHandlerFunc)  { h.add("PATCH", p, f) }

func found(tm *wickplugin.ToolModule) connplugin.Found {
	return connplugin.Found{Key: "demo", BinaryPath: "/bin/true",
		Manifest: wickplugin.Manifest{Version: "1.2.3", Kind: wickplugin.KindTool, Tool: tm}}
}

func setup(t *testing.T, pl *plugin, tm *wickplugin.ToolModule) (*Pool, *recRouter) {
	t.Helper()
	pool := NewPool()
	t.Cleanup(pool.KillAll)
	mod := pool.buildModule(found(tm), pl.spawn, sockDir(t))
	rr := &recRouter{h: map[string]tool.HandlerFunc{}, wh: map[string]tool.WebhookHandlerFunc{}}
	mod.Register(rr)
	return pool, rr
}

type cfgMap map[string]string

func (c cfgMap) GetOwned(_, k string) string { return c[k] }
func (c cfgMap) Missing(string) []string     { return nil }

// call runs a session-gated route as wick would: a Ctx with a page renderer
// that marks the layout so the wrap is visible.
func call(rr *recRouter, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	render := func(c *tool.Ctx, body templ.Component) {
		io.WriteString(c.W, "<layout>")
		body.Render(c.R.Context(), c.W)
		io.WriteString(c.W, "</layout>")
	}
	meta := tool.Tool{Key: "demo", Name: "Demo", Path: "/tools/demo"}
	rr.h["GET /{path...}"](tool.NewCtx(rec, req, render, meta, cfgMap{"token": "s3"}, nil))
	return rec
}

func withUser(t *testing.T) {
	tool.SetUserResolver(func(*http.Request) (tool.User, bool) {
		return tool.User{ID: "42", Email: "op@example.test", IsAdmin: true, Tags: []string{"ops"}}, true
	})
	t.Cleanup(func() { tool.SetUserResolver(nil) })
}

func TestProxyInjectsTrustedHeadersAndDropsSpoofed(t *testing.T) {
	withUser(t)
	pl := &plugin{h: echoHandler()}
	_, rr := setup(t, pl, &wickplugin.ToolModule{Configs: nil})
	req := httptest.NewRequest("GET", "/tools/demo/api", nil)
	req.Header.Set("X-Wick-User-Id", "1")
	req.Header.Set("X-Wick-User-Role", "admin")
	req.Header.Set("X-Wick-Evil", "x")
	rec := call(rr, req)
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	if got["X-Wick-User-Id"] != "42" || got["X-Wick-User-Role"] != "admin" || got["X-Wick-Base"] != "/tools/demo" || got["X-Wick-User-Tags"] != "ops" {
		t.Fatalf("headers = %v", got)
	}
	if _, ok := got["X-Wick-Evil"]; ok {
		t.Fatal("client X-Wick-* header reached the plugin")
	}
	if strings.Contains(rec.Body.String(), "<layout>") {
		t.Fatal("JSON must not be wrapped")
	}
}

func TestLayoutWrapAndHTMXPassThrough(t *testing.T) {
	pl := &plugin{h: echoHandler()}
	_, rr := setup(t, pl, &wickplugin.ToolModule{})
	rec := call(rr, httptest.NewRequest("GET", "/tools/demo/page", nil))
	if rec.Body.String() != "<layout><p>frag</p></layout>" || rec.Header().Get(wickplugin.HeaderLayout) != "" {
		t.Fatalf("wrapped = %q %v", rec.Body.String(), rec.Header())
	}
	req := httptest.NewRequest("GET", "/tools/demo/page", nil)
	req.Header.Set("HX-Request", "true")
	if rec := call(rr, req); rec.Body.String() != "<p>frag</p>" {
		t.Fatalf("htmx = %q", rec.Body.String())
	}
}

func TestStaticGetsVersionCacheAndRevalidatesWithoutPlugin(t *testing.T) {
	pl := &plugin{h: echoHandler()}
	pool, rr := setup(t, pl, &wickplugin.ToolModule{})
	rec := call(rr, httptest.NewRequest("GET", "/tools/demo/static/app.css", nil))
	if rec.Header().Get("Cache-Control") == "" || rec.Header().Get("ETag") != `W/"demo-1.2.3"` {
		t.Fatalf("static headers = %v", rec.Header())
	}
	pool.runners["demo"].Stop()
	before := pl.n.Load()
	req := httptest.NewRequest("GET", "/tools/demo/static/app.css", nil)
	req.Header.Set("If-None-Match", `W/"demo-1.2.3"`)
	if rec := call(rr, req); rec.Code != http.StatusNotModified || pl.n.Load() != before {
		t.Fatalf("revalidate code=%d spawns=%d->%d", rec.Code, before, pl.n.Load())
	}
}

func TestWebhookOnlyDeclaredRoutesSkipSession(t *testing.T) {
	pl := &plugin{h: echoHandler()}
	tm := &wickplugin.ToolModule{Webhooks: []wickplugin.ToolWebhook{{Method: "POST", Path: "/webhook/hook", Group: "/webhook"}}}
	_, rr := setup(t, pl, tm)
	// Only the declared group is opened; the tool root stays on the gated chain.
	if len(rr.groups) != 1 || rr.groups[0] != "/webhook" || len(rr.hooks) != 1 || rr.hooks[0] != "POST /webhook/hook" {
		t.Fatalf("groups=%v hooks=%v", rr.groups, rr.hooks)
	}
	// The declared route proxies with no user headers.
	withUser(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/tools/demo/webhook/hook", nil)
	req.Header.Set("X-Wick-User-Id", "1")
	rr.wh["POST /webhook/hook"](tool.NewWebhookCtx(rec, req, tool.Tool{Key: "demo"}, cfgMap{}))
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if _, ok := got["X-Wick-User-Id"]; ok || got["X-Wick-Base"] != "/tools/demo" {
		t.Fatalf("webhook headers = %v", got)
	}
	// An undeclared path under the open group must not reach the plugin.
	if rec := call(rr, httptest.NewRequest("GET", "/tools/demo/webhook/other", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("undeclared webhook path code = %d", rec.Code)
	}
}

func TestConfigPushedOnceAndAgainOnChange(t *testing.T) {
	pl := &plugin{h: echoHandler()}
	run := newRunner("demo", "x", sockDir(t), false, pl.spawn)
	defer run.Stop()
	for _, cfg := range []map[string]string{{"a": "1"}, {"a": "1"}, {"a": "2"}} {
		_, rel, err := run.Acquire(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		rel()
	}
	if len(pl.conn.cfg) != 2 || pl.conn.cfg[1]["a"] != "2" {
		t.Fatalf("configure calls = %v", pl.conn.cfg)
	}
}

func TestSingleflightSpawn(t *testing.T) {
	pl := &plugin{h: echoHandler(), delay: 100 * time.Millisecond}
	run := newRunner("demo", "x", sockDir(t), false, pl.spawn)
	defer run.Stop()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, rel, err := run.Acquire(context.Background(), nil)
			if err != nil {
				errs <- err
				return
			}
			rel()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := pl.n.Load(); n != 1 {
		t.Fatalf("spawns = %d, want 1", n)
	}
}

func TestHoldTimeoutAnswers503(t *testing.T) {
	pl := &plugin{h: echoHandler(), delay: 300 * time.Millisecond}
	pool, rr := setup(t, pl, &wickplugin.ToolModule{})
	pool.runners["demo"].hold = 50 * time.Millisecond
	start := time.Now()
	rec := call(rr, httptest.NewRequest("GET", "/tools/demo/api", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "starting") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatal("request was not released at the hold limit")
	}
	// The spawn finishes in the background; the next request is served.
	time.Sleep(350 * time.Millisecond)
	if rec := call(rr, httptest.NewRequest("GET", "/tools/demo/api", nil)); rec.Code != http.StatusOK || pl.n.Load() != 1 {
		t.Fatalf("after warmup code=%d spawns=%d", rec.Code, pl.n.Load())
	}
}

func TestIdleKillSkipsInflightAndKeepWarm(t *testing.T) {
	pl := &plugin{h: echoHandler()}
	run := newRunner("demo", "x", sockDir(t), false, pl.spawn)
	defer run.Stop()
	clock := time.Now()
	run.now = func() time.Time { return clock }
	_, rel, err := run.Acquire(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	if run.sweep(time.Minute) {
		t.Fatal("killed with a request in flight (SSE/websocket)")
	}
	rel()
	clock = clock.Add(time.Hour)
	if !run.sweep(time.Minute) || run.Running() {
		t.Fatal("idle plugin not killed")
	}

	warm := newRunner("warm", "x", sockDir(t), true, pl.spawn)
	defer warm.Stop()
	warm.now = run.now
	if err := warm.Warm(); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(24 * time.Hour)
	if warm.sweep(time.Minute) || !warm.Running() {
		t.Fatal("keep_warm plugin was idle-killed")
	}
}

func TestLoadSkipsUnverified(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "demo"), 0o755)
	os.WriteFile(filepath.Join(dir, "demo", "plugin.json"), []byte(`{"kind":"tool","version":"1","entry":"demo","module":{"meta":{"key":"demo"}}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "demo", "demo"), []byte("x"), 0o755)
	n := NewPool().Load(dir, nil, nil, func(tool.Module) { t.Fatal("unverified plugin registered") })
	if n != 0 {
		t.Fatalf("loaded %d", n)
	}
}
