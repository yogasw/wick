package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	pluginreplace "github.com/yogasw/wick/internal/plugins/replace"
	"github.com/yogasw/wick/internal/tools"
	"github.com/yogasw/wick/pkg/tool"
)

// gateHeader stands in for RequireToolAccess where a test only needs to tell
// the gated and the webhook path apart.
func gateHeader(meta login.ToolMeta, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Auth") == "" {
			http.Error(w, "denied "+meta.Path, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func demoPluginModule(key string) tool.Module {
	return tool.Module{
		Meta: tool.Tool{Key: key, Name: key, Icon: "x", DefaultVisibility: entity.VisibilityPrivate},
		Register: func(r tool.Router) {
			r.GET("/", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusOK) })
			r.GET("/{path...}", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusOK) })
			r.WebhookGroup("/webhook").POST("/in", func(c *tool.WebhookCtx) { c.W.WriteHeader(http.StatusAccepted) })
		},
	}
}

func serve(h http.Handler, method, path string, auth bool) int {
	req := httptest.NewRequest(method, path, nil)
	if auth {
		req.Header.Set("X-Test-Auth", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestLiveToolSinkHotLoadDisableEnable(t *testing.T) {
	const key = "t17-live-demo"
	routes := newPluginToolRoutes(nil, gateHeader)
	var seededCfg, seededTags []string
	sink := newLiveToolSink(routes)
	sink.ensureConfigs = func(_ context.Context, owner string, rows ...entity.Config) error {
		seededCfg = append(seededCfg, owner)
		return nil
	}
	sink.ensureTags = func(_ context.Context, path string, _ []tool.DefaultTag) error {
		seededTags = append(seededTags, path)
		return nil
	}
	builtinFallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	h := routes.wrap(builtinFallback)

	if got := serve(h, "GET", "/tools/"+key, true); got != http.StatusNotFound {
		t.Fatalf("before install: %d", got)
	}

	m := demoPluginModule(key)
	m.Configs = []entity.Config{{Key: "token"}}
	m.Meta.DefaultTags = []tool.DefaultTag{{Name: "Demo"}}
	if err := sink.Add(m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tools.Unregister(key) })

	if got := serve(h, "GET", "/tools/"+key, true); got != http.StatusOK {
		t.Fatalf("hot-loaded root: %d", got)
	}
	if got := serve(h, "GET", "/tools/"+key+"/a/b", true); got != http.StatusOK {
		t.Fatalf("hot-loaded subpath: %d", got)
	}
	if got := serve(h, "GET", "/tools/"+key, false); got != http.StatusFound {
		t.Fatalf("gate skipped: %d", got)
	}
	if got := serve(h, "POST", "/tools/"+key+"/webhook/in", false); got != http.StatusAccepted {
		t.Fatalf("webhook: %d", got)
	}
	if len(routes.WebhookRoutes()) != 1 {
		t.Fatalf("webhook routes %v", routes.WebhookRoutes())
	}
	if len(seededCfg) != 1 || seededCfg[0] != key || len(seededTags) != 1 || seededTags[0] != "/tools/"+key {
		t.Fatalf("seeded cfg=%v tags=%v", seededCfg, seededTags)
	}
	if !hasItem(liveToolItems(), key) {
		t.Fatal("hot-loaded tool missing from liveToolItems")
	}

	sink.Remove(key)
	if got := serve(h, "GET", "/tools/"+key, true); got != http.StatusNotFound {
		t.Fatalf("after disable: %d", got)
	}
	if hasItem(liveToolItems(), key) || tools.Has(key) {
		t.Fatal("disabled tool still listed")
	}

	if err := sink.Add(demoPluginModule(key)); err != nil {
		t.Fatal(err)
	}
	if got := serve(h, "GET", "/tools/"+key, true); got != http.StatusOK {
		t.Fatalf("after enable: %d", got)
	}
}

func TestValidatePluginModuleRefusesBuiltinKeyAndBadModules(t *testing.T) {
	const key = "t17-builtin"
	tools.Register(tool.Module{Meta: tool.Tool{Key: key, Name: key, Icon: "x"}, Register: func(tool.Router) {}})
	t.Cleanup(func() { tools.Unregister(key) })
	routes := newPluginToolRoutes(nil, gateHeader)
	if err := newLiveToolSink(routes).Add(demoPluginModule(key)); err == nil {
		t.Fatal("plugin took over a built-in key")
	}
	if routes.lookup("/tools/"+key) != nil {
		t.Fatal("refused plugin was mounted")
	}
	// The same check boot uses (B2): a colliding key is an error, not a fatal.
	if err := validatePluginModule(demoPluginModule(key)); err == nil {
		t.Fatal("boot check accepted a built-in key")
	}
	bad := demoPluginModule("t17-bad")
	bad.Meta.Icon = ""
	if err := validatePluginModule(bad); err == nil {
		t.Fatal("invalid meta accepted")
	}
	dupRoutes := demoPluginModule("t17-dup")
	dupRoutes.Register = func(r tool.Router) {
		r.GET("/", func(*tool.Ctx) {})
		r.GET("/", func(*tool.Ctx) {})
	}
	if err := validatePluginModule(dupRoutes); err == nil {
		t.Fatal("duplicate routes accepted")
	}
	if tools.Has("t17-bad") || tools.Has("t17-dup") {
		t.Fatal("refused module registered")
	}
}

// The real access middleware, wired as server.go does: plugin paths go
// through RequireToolAccess for that one tool, webhook groups skip it, and
// once the plugin is gone the path falls to the built-in chain (404).
func TestPluginRoutesRealAccessCheck(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	authMidd := login.NewMiddleware(login.NewService(db, ""), nil)

	const key = "t17-acl"
	tag := entity.Tag{Name: "ops", IsFilter: true}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: "/tools/" + key, TagID: tag.ID}).Error; err != nil {
		t.Fatal(err)
	}

	routes := newPluginToolRoutes(nil, func(meta login.ToolMeta, next http.Handler) http.Handler {
		return authMidd.RequireToolAccess([]login.ToolMeta{meta})(next)
	})
	builtinMux := http.NewServeMux()
	fallback := authMidd.RequireToolAccess(nil)(builtinMux) // no built-in under this key
	h := routes.wrap(fallback)
	sink := newLiveToolSink(routes)
	if err := sink.Add(demoPluginModule(key)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tools.Unregister(key) })

	do := func(path, method string, u *entity.User, tagIDs []string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if u != nil {
			req = req.WithContext(login.WithUser(req.Context(), u, tagIDs))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("/tools/"+key, "GET", nil, nil); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/auth/login" {
		t.Fatalf("anonymous: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	plain := &entity.User{ID: "u1", Approved: true}
	if rec := do("/tools/"+key, "GET", plain, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("user without the tag: %d", rec.Code)
	}
	admin := &entity.User{ID: "u2", Approved: true, Role: entity.RoleAdmin}
	if rec := do("/tools/"+key+"/x", "GET", admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin: %d", rec.Code)
	}
	if rec := do("/tools/"+key+"/webhook/in", "POST", nil, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("webhook without login: %d", rec.Code)
	}
	sink.Remove(key)
	if rec := do("/tools/"+key, "GET", admin, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled plugin: %d", rec.Code)
	}
}

// S1: a plugin that replaces a tool takes it over live (old hidden, paths
// 404), and unloading the plugin brings the old tool back.
func TestLiveReplaceAndRestore(t *testing.T) {
	const oldKey, newKey = "t17-old", "t17-new"
	builtin := tool.Module{Meta: tool.Tool{Key: oldKey, Name: oldKey, Icon: "x", DefaultVisibility: entity.VisibilityPrivate},
		Register: func(r tool.Router) { r.GET("/", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusTeapot) }) }}
	tools.Register(builtin)
	t.Cleanup(func() { tools.Unregister(oldKey); tools.Unregister(newKey); pluginreplace.Forget(newKey) })

	// The built-in sits on the boot mux, like server.go mounts it.
	bootMux := http.NewServeMux()
	tr := newToolRouter(nil)
	tr.withScope(tool.Tool{Key: oldKey, Name: oldKey, Path: "/tools/" + oldKey}, false, builtin.Register)
	tr.mount(bootMux)
	routes := newPluginToolRoutes(nil, func(_ login.ToolMeta, next http.Handler) http.Handler { return next })
	h := routes.wrap(bootMux)

	var migrated []pluginreplace.Pair
	sink := newLiveToolSink(routes)
	sink.migrate = func(_ context.Context, pairs []pluginreplace.Pair) { migrated = append(migrated, pairs...) }
	m := demoPluginModule(newKey)
	m.Meta.Replaces = []string{oldKey}
	if err := sink.Add(m); err != nil {
		t.Fatal(err)
	}
	if tools.Has(oldKey) || serve(h, "GET", "/tools/"+oldKey, true) != http.StatusNotFound {
		t.Fatal("replaced tool still listed or routable")
	}
	if len(migrated) != 1 || migrated[0].Old != oldKey || len(pluginreplace.Lookup(newKey)) != 1 {
		t.Fatalf("migration %v / active pairs %v", migrated, pluginreplace.Lookup(newKey))
	}

	sink.Remove(newKey)
	if !tools.Has(oldKey) || serve(h, "GET", "/tools/"+oldKey, true) != http.StatusTeapot {
		t.Fatal("replaced tool not restored after the plugin was disabled")
	}
	if len(pluginreplace.Lookup(newKey)) != 0 {
		t.Fatal("replace pair still active")
	}
}

// A tool replaced at boot was never mounted: restoring it mounts it once,
// and a second disable/enable cycle reuses that mount.
func TestRestoreBootReplacedMountsOnce(t *testing.T) {
	const oldKey, newKey = "t17-bootold", "t17-bootnew"
	registers := 0
	builtin := tool.Module{Meta: tool.Tool{Key: oldKey, Name: oldKey, Icon: "x"},
		Register: func(r tool.Router) {
			registers++
			r.GET("/", func(c *tool.Ctx) { c.W.WriteHeader(http.StatusTeapot) })
		}}
	routes := newPluginToolRoutes(nil, func(_ login.ToolMeta, next http.Handler) http.Handler { return next })
	h := routes.wrap(http.NotFoundHandler())
	sink := newLiveToolSink(routes)
	m := demoPluginModule(newKey)
	m.Meta.Replaces = []string{oldKey}
	// Boot: both registered, Prepare drops the old one.
	tools.Register(builtin)
	before := tools.All()
	tools.Register(m)
	pairs := pluginreplace.Prepare(nil, tools.All(), nil, tools.Unregister)
	t.Cleanup(func() { tools.Unregister(oldKey); tools.Unregister(newKey); pluginreplace.Forget(newKey) })
	sink.rememberBootReplaced(before, pairs)
	if err := routes.set(m); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		sink.Remove(newKey)
		if serve(h, "GET", "/tools/"+oldKey, true) != http.StatusTeapot || !tools.Has(oldKey) {
			t.Fatalf("cycle %d: boot-replaced tool not restored", i)
		}
		if err := sink.Add(m); err != nil {
			t.Fatal(err)
		}
		if serve(h, "GET", "/tools/"+oldKey, true) != http.StatusNotFound || tools.Has(oldKey) {
			t.Fatalf("cycle %d: not replaced again", i)
		}
	}
	if registers != 1 {
		t.Fatalf("replaced tool registered %d times, want 1", registers)
	}
}

func TestPluginReloadersSkipsNil(t *testing.T) {
	pluginReloaders{}.Reload(context.Background())
}

func hasItem(items []tool.Tool, key string) bool {
	for _, it := range items {
		if it.Key == key && it.Path == "/tools/"+key {
			return true
		}
	}
	return false
}
