package hostapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type adminDir map[string]PluginInfo

func (d adminDir) Plugin(key string) (PluginInfo, bool) { p, ok := d[key]; return p, ok }
func (d adminDir) All() []PluginInfo {
	var out []PluginInfo
	for _, p := range d {
		out = append(out, p)
	}
	return out
}

// fullPlugin declares every resource kind plus a scope this wick does not
// know.
func fullPlugin() PluginInfo {
	return PluginInfo{Key: "syncer", Kind: wickplugin.KindJob, Version: "1.2.0",
		Scopes: []string{"users.read", "rest.read", "rest.write", "db.read", "db.write", "connectors.execute",
			"config.read_other", "config.read_secret", "datadir", "future.scope"},
		Resources: &wickplugin.HostResources{
			Rest:       []wickplugin.HostRestResource{{Method: "GET", Path: "/api/tickets/*"}, {Method: "POST", Path: "/api/tickets"}},
			DB:         []wickplugin.HostDBResource{{Table: "users", Mode: "read"}, {Table: "tickets", Mode: "write"}},
			Connectors: []wickplugin.HostConnectorResource{{Connector: "slack", Ops: []string{"lookup_user_by_email", "send_message"}}},
			Config:     []wickplugin.HostConfigResource{{Owner: "job:notion-ticket-sync", Key: "token"}},
		}}
}

type adminFixture struct {
	admin   *Admin
	store   *GormStore
	mux     *http.ServeMux
	revoked []string
	flag    map[string]bool
	now     time.Time
}

func newAdminFixture(t *testing.T, wrap func(http.Handler) http.Handler) *adminFixture {
	t.Helper()
	f := &adminFixture{store: newGormStore(t), flag: map[string]bool{}, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	other := PluginInfo{Key: "plain", Kind: wickplugin.KindTool}
	f.admin = &Admin{
		Plugins: adminDir{"syncer": fullPlugin(), "plain": other},
		Perms:   NewPermissions(NewMemoryPermissionStore()),
		Calls:   f.store,
		Op:      func(c, op string) (bool, bool) { return c == "slack" && op == "send_message", true },
		Revoke: func(key string, revoked bool) (int, bool, error) {
			f.revoked = append(f.revoked, key)
			changed := f.flag[key] != revoked
			f.flag[key] = revoked
			return 2, changed, nil
		},
		Revoked: func(key string) bool { return f.flag[key] },
		Actor:   func(*http.Request) string { return "admin-1" },
		now:     func() time.Time { return f.now },
	}
	if wrap == nil {
		wrap = func(h http.Handler) http.Handler { return h }
	}
	f.mux = http.NewServeMux()
	f.admin.RegisterAdmin(f.mux, wrap)
	return f
}

func (f *adminFixture) do(t *testing.T, method, path string, body any) (*httptest.ResponseRecorder, PluginAccess) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	var v PluginAccess
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return rec, v
}

func scopeOf(v PluginAccess, name string) *AccessScope {
	for i := range v.Scopes {
		if v.Scopes[i].Scope == name {
			return &v.Scopes[i]
		}
	}
	return nil
}

func TestAdminViewEveryResourceKind(t *testing.T) {
	f := newAdminFixture(t, nil)
	_ = f.store.WriteCalls(context.Background(), []entity.PluginHostCall{
		{Ts: f.now.Add(-time.Hour), PluginKey: "syncer", RPC: "ListTickets", Scope: "tickets.read", OK: true},
		{Ts: f.now.Add(-2 * time.Hour), PluginKey: "syncer", RPC: "Query", Scope: "db.read", OK: false, Code: "PermissionDenied"},
		{Ts: f.now.Add(-3 * time.Hour), PluginKey: "syncer", RPC: "Query", Scope: "db.read", OK: false, Code: "InvalidArgument"},
	})
	rec, v := f.do(t, "GET", AdminBase+"/syncer", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	want := map[string][]string{
		"rest.read":          {"GET /api/tickets/*"},
		"rest.write":         {"POST /api/tickets"},
		"db.read":            {"users:read", "tickets:read"},
		"db.write":           {"tickets:write"},
		"connectors.execute": {"slack.lookup_user_by_email", "slack.send_message"},
		"config.read_secret": {"job:notion-ticket-sync/token"},
	}
	for scope, items := range want {
		s := scopeOf(v, scope)
		if s == nil {
			t.Fatalf("scope %s missing", scope)
		}
		var got []string
		for _, it := range s.Items {
			got = append(got, it.Item)
		}
		if !slices.Equal(got, items) {
			t.Errorf("%s items = %v, want %v", scope, got, items)
		}
	}
	if v.ItemCount != 8 {
		t.Errorf("item_count = %d, want 8", v.ItemCount)
	}
	if s := scopeOf(v, "db.read"); s.Status != StatusOffByDefault || s.Denied != 1 || s.Failed != 2 {
		t.Errorf("db.read = %+v, want off-by-default with 2 failed calls, 1 denied", s)
	}
	if s := scopeOf(v, "config.read_secret"); !s.Sensitive || s.Status != StatusOffByDefault {
		t.Errorf("config.read_secret = %+v, want sensitive + off by default", s)
	}
	if s := scopeOf(v, "rest.write"); !s.Destructive || !s.Items[0].Destructive {
		t.Errorf("rest.write not destructive: %+v", s)
	}
	conn := scopeOf(v, "connectors.execute")
	if conn.Items[0].Destructive || !conn.Items[1].Destructive {
		t.Errorf("connector destructive flags = %+v, want only send_message", conn.Items)
	}
	if s := scopeOf(v, "future.scope"); s == nil || s.Status != StatusUnknown || s.Reason != unknownScopeNote {
		t.Errorf("unknown scope row = %+v", s)
	}
	// Used without being declared: shown as undeclared.
	if s := scopeOf(v, "tickets.read"); s == nil || s.Declared || s.Status != StatusUndeclared || s.Calls != 1 {
		t.Errorf("tickets.read row = %+v, want undeclared with 1 call", s)
	}
	if s := scopeOf(v, "sessions.read"); s != nil {
		t.Errorf("unused undeclared scope listed: %+v", s)
	}
	if v.Calls != 3 || v.Failed != 2 || v.Denied != 1 || v.LastCall == nil || !v.LastCall.Equal(f.now.Add(-time.Hour)) {
		t.Errorf("counters calls=%d denied=%d last=%v", v.Calls, v.Denied, v.LastCall)
	}

	// The list only carries plugins that touch the Host API.
	rec = httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("GET", AdminBase, nil))
	var list []PluginAccess
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Key != "syncer" {
		t.Fatalf("list = %+v, want only syncer", list)
	}
	// The detail card still answers for a plugin without declarations.
	if rec, v := f.do(t, "GET", AdminBase+"/plain", nil); rec.Code != 200 || v.Declares || len(v.Scopes) != 0 {
		t.Errorf("plain = %d %+v", rec.Code, v)
	}
}

func TestAdminScopeToggle(t *testing.T) {
	f := newAdminFixture(t, nil)
	ctx := context.Background()
	p := fullPlugin()
	on, off := true, false
	rec, v := f.do(t, "POST", AdminBase+"/syncer/scope", map[string]any{"scope": "db.read", "allowed": on})
	if rec.Code != 200 || scopeOf(v, "db.read").Status != StatusAllowed || scopeOf(v, "db.read").Overlay != "allow" {
		t.Fatalf("allow db.read: %d %+v", rec.Code, scopeOf(v, "db.read"))
	}
	if d := f.admin.Perms.Decide(ctx, p, "db.read", "users:read"); !d.Allowed {
		t.Fatalf("Permissions did not read the toggle: %+v", d)
	}
	f.do(t, "POST", AdminBase+"/syncer/scope", map[string]any{"scope": "users.read", "allowed": off})
	if d := f.admin.Perms.Decide(ctx, p, "users.read", ""); d.Allowed || d.Status != StatusBlocked {
		t.Fatalf("users.read OFF not applied: %+v", d)
	}
	_, v = f.do(t, "POST", AdminBase+"/syncer/scope", map[string]any{"scope": "db.read", "allowed": nil})
	if s := scopeOf(v, "db.read"); s.Status != StatusOffByDefault || s.Overlay != "" {
		t.Fatalf("reset db.read = %+v", s)
	}
	for _, bad := range []string{"future.scope", "whoami", "nope"} {
		if rec, _ := f.do(t, "POST", AdminBase+"/syncer/scope", map[string]any{"scope": bad, "allowed": on}); rec.Code != 400 {
			t.Errorf("scope %q accepted: %d", bad, rec.Code)
		}
	}
	if rec, _ := f.do(t, "POST", AdminBase+"/ghost/scope", map[string]any{"scope": "db.read", "allowed": on}); rec.Code != 404 {
		t.Errorf("unknown plugin = %d", rec.Code)
	}
}

func TestAdminItemBlock(t *testing.T) {
	f := newAdminFixture(t, nil)
	ctx := context.Background()
	p := fullPlugin()
	body := map[string]any{"scope": "connectors.execute", "item": "slack.send_message", "blocked": true}
	rec, v := f.do(t, "POST", AdminBase+"/syncer/item", body)
	if rec.Code != 200 || !scopeOf(v, "connectors.execute").Items[1].Blocked {
		t.Fatalf("block item: %d %s", rec.Code, rec.Body)
	}
	if d := f.admin.Perms.Decide(ctx, p, "connectors.execute", "slack.send_message"); d.Allowed {
		t.Fatal("blocked item still allowed")
	}
	if d := f.admin.Perms.Decide(ctx, p, "connectors.execute", "slack.lookup_user_by_email"); !d.Allowed {
		t.Fatalf("sibling item blocked too: %+v", d)
	}
	body["blocked"] = false
	f.do(t, "POST", AdminBase+"/syncer/item", body)
	if d := f.admin.Perms.Decide(ctx, p, "connectors.execute", "slack.send_message"); !d.Allowed {
		t.Fatalf("↺ did not allow again: %+v", d)
	}
	for _, b := range []map[string]any{
		{"scope": "connectors.execute", "item": "slack.delete_channel", "blocked": true}, // not declared
		{"scope": "db.read", "item": "tickets:write", "blocked": true},                   // other scope's item
		{"scope": "users.read", "item": "x", "blocked": true},                            // scope without items
	} {
		if rec, _ := f.do(t, "POST", AdminBase+"/syncer/item", b); rec.Code != 400 {
			t.Errorf("%v accepted: %d", b, rec.Code)
		}
	}
}

func TestAdminBlockAllAndRevoke(t *testing.T) {
	f := newAdminFixture(t, nil)
	ctx := context.Background()
	p := fullPlugin()
	rec, v := f.do(t, "POST", AdminBase+"/syncer/block-all", map[string]any{"blocked": true})
	if rec.Code != 200 || !v.BlockedAll {
		t.Fatalf("block all: %d %+v", rec.Code, v)
	}
	if d := f.admin.Perms.Decide(ctx, p, "users.read", ""); d.Allowed {
		t.Fatal("block all did not deny")
	}
	_, v = f.do(t, "POST", AdminBase+"/syncer/block-all", map[string]any{"blocked": false})
	if v.BlockedAll || !f.admin.Perms.Decide(ctx, p, "users.read", "").Allowed {
		t.Fatal("unblock did not restore access")
	}
	revoke := func(key string, body any) (dropped int, changed bool, v PluginAccess) {
		t.Helper()
		rec, _ := f.do(t, "POST", AdminBase+"/"+key+"/revoke", body)
		if rec.Code != 200 {
			t.Fatalf("revoke %s = %d %s", key, rec.Code, rec.Body)
		}
		var out struct {
			Access  PluginAccess `json:"access"`
			Dropped int          `json:"dropped"`
			Changed bool         `json:"changed"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Dropped, out.Changed, out.Access
	}
	if d, c, v := revoke("syncer", nil); d != 2 || !c || !v.Revoked {
		t.Fatalf("revoke = dropped %d changed %v revoked %v", d, c, v.Revoked)
	}
	if _, c, _ := revoke("syncer", map[string]any{"revoked": true}); c {
		t.Fatal("second revoke reported a change")
	}
	if _, c, v := revoke("syncer", map[string]any{"revoked": false}); !c || v.Revoked {
		t.Fatalf("restore: changed %v revoked %v", c, v.Revoked)
	}
	if _, _, v := revoke("plain", nil); !v.Revoked {
		t.Fatal("revoke of a tool plugin not shown")
	}
	if !slices.Equal(f.revoked, []string{"syncer", "syncer", "syncer", "plain"}) {
		t.Fatalf("Revoke calls = %v", f.revoked)
	}
	f.admin.Revoke = nil
	if rec, _ := f.do(t, "POST", AdminBase+"/syncer/revoke", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoke without host api = %d", rec.Code)
	}
}

func TestAdminDeclarativeAndUndeclaredScopes(t *testing.T) {
	f := newAdminFixture(t, nil)
	for _, c := range []struct {
		scope   string
		allowed any
		code    int
	}{
		{"datadir", false, 400},       // declarative: nothing to decide
		{"net.listen", true, 400},     // declarative, even undeclared
		{"tickets.write", false, 200}, // no resource: any official scope
		{"connectors.read", true, 200},
	} {
		if rec, _ := f.do(t, "POST", AdminBase+"/syncer/scope", map[string]any{"scope": c.scope, "allowed": c.allowed}); rec.Code != c.code {
			t.Errorf("%s=%v: %d, want %d", c.scope, c.allowed, rec.Code, c.code)
		}
	}
	// plain declares nothing: a resource scope cannot be switched on, but a
	// leftover row can be cleared.
	if rec, _ := f.do(t, "POST", AdminBase+"/plain/scope", map[string]any{"scope": "db.read", "allowed": true}); rec.Code != 400 {
		t.Errorf("undeclared db.read accepted: %d", rec.Code)
	}
	if rec, _ := f.do(t, "POST", AdminBase+"/plain/scope", map[string]any{"scope": "db.read", "allowed": nil}); rec.Code != 200 {
		t.Errorf("clearing undeclared db.read = %d", rec.Code)
	}
}

func TestAdminUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUnavailableAdmin(mux, func(h http.Handler) http.Handler { return h })
	for _, c := range []struct{ method, path string }{
		{"GET", AdminBase}, {"GET", AdminBase + "/x"}, {"POST", AdminBase + "/x/revoke"}, {"GET", AdminCallsPath},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), HostAPINotStarted) {
			t.Errorf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestAdminRejectsNonAdmin(t *testing.T) {
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "forbidden", http.StatusForbidden) })
	}
	f := newAdminFixture(t, deny)
	for _, c := range []struct{ method, path string }{
		{"GET", AdminBase}, {"GET", AdminBase + "/syncer"}, {"POST", AdminBase + "/syncer/scope"},
		{"POST", AdminBase + "/syncer/item"}, {"POST", AdminBase + "/syncer/block-all"},
		{"POST", AdminBase + "/syncer/revoke"}, {"GET", AdminCallsPath},
	} {
		if rec, _ := f.do(t, c.method, c.path, map[string]any{"scope": "db.read", "allowed": true, "blocked": true}); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if len(f.revoked) != 0 {
		t.Fatal("revoke ran for a non-admin")
	}
	if d := f.admin.Perms.Decide(context.Background(), fullPlugin(), "db.read", "users:read"); d.Allowed {
		t.Fatal("a non-admin toggle was applied")
	}
}

func TestAdminCallsFilter(t *testing.T) {
	f := newAdminFixture(t, nil)
	var rows []entity.PluginHostCall
	for i := range 5 {
		rows = append(rows, entity.PluginHostCall{Ts: f.now.Add(-time.Duration(i+1) * time.Minute), PluginKey: "syncer",
			RPC: "GetUser", Scope: "users.read", OK: i%2 == 0, Code: map[bool]string{false: "PermissionDenied"}[i%2 == 0], DurationMs: int64(i)})
	}
	rows = append(rows,
		entity.PluginHostCall{Ts: f.now.Add(-time.Minute), PluginKey: "other", RPC: "Whoami", Scope: "whoami", OK: true},
		entity.PluginHostCall{Ts: f.now.Add(-8 * 24 * time.Hour), PluginKey: "syncer", RPC: "GetUser", Scope: "users.read", OK: false})
	if err := f.store.WriteCalls(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	get := func(q string) (calls []CallView, total int64) {
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, httptest.NewRequest("GET", AdminCallsPath+q, nil))
		if rec.Code != 200 {
			t.Fatalf("%s = %d %s", q, rec.Code, rec.Body)
		}
		var out struct {
			Calls []CallView `json:"calls"`
			Total int64      `json:"total"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Calls, out.Total
	}
	if _, total := get(""); total != 6 {
		t.Errorf("all = %d, want 6 (the 8-day-old row is outside the window)", total)
	}
	calls, total := get("?plugin=syncer&failed=1")
	if total != 2 || len(calls) != 2 || calls[0].OK || calls[0].Code != "PermissionDenied" {
		t.Errorf("failed syncer = %d %+v", total, calls)
	}
	calls, total = get("?plugin=syncer&page=2&limit=2")
	if total != 5 || len(calls) != 2 || calls[0].DurationMs != 2 {
		t.Errorf("page 2 = %d %+v (want newest-first, 3rd and 4th)", total, calls)
	}
}
