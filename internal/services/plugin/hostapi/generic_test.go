package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// genPlugin declared the whole generic layer.
var genPlugin = PluginInfo{Key: "gen", Kind: "connector", ConfigOwner: "connector:gen",
	Scopes: []string{"users.read", "tickets.read", "connectors.read", "connectors.execute", "rest.read", "rest.write", "db.read", "db.write"},
	Resources: &wickplugin.HostResources{
		Rest: []wickplugin.HostRestResource{
			{Method: "GET", Path: "/api/tickets/*"}, {Method: "GET", Path: "/api/things/**"},
			{Method: "GET", Path: "/api/redirect/*"}, {Method: "POST", Path: "/api/tickets"},
		},
		DB:         []wickplugin.HostDBResource{{Table: "tickets", Mode: "read"}, {Table: "notes", Mode: "write"}},
		Connectors: []wickplugin.HostConnectorResource{{Connector: "slack", Ops: []string{"lookup_user_by_email", "send_message", "ghost"}}},
	}}

// allowAll turns on gen's default-off scopes, as an admin would.
func allowAll(t *testing.T, f *fixture) {
	t.Helper()
	for _, sc := range []string{"rest.write", "db.read", "db.write"} {
		if err := f.perms.Set(context.Background(), "gen", sc, "", true, "admin"); err != nil {
			t.Fatal(err)
		}
	}
}

type fakePrincipals struct{ users *fakeUsers }

func (f fakePrincipals) GetUserByID(_ context.Context, id string) (*entity.User, error) {
	for _, u := range f.users.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, errors.New("not found")
}

func (f fakePrincipals) GetUserFilterTagIDs(_ context.Context, id string) []string {
	return []string{"tag-" + id}
}

// fakeConns is a ConnectorRunner over fixed instances.
type fakeConns struct {
	mu        sync.Mutex
	instances []*hostpb.ConnectorInstance
	calls     []ConnectorCall
	lastUser  string
	lastTags  []string
}

func newFakeConns() *fakeConns {
	return &fakeConns{instances: []*hostpb.ConnectorInstance{{Id: "inst-1", Connector: "slack", Label: "Slack", Status: "enabled", Ops: []string{"lookup_user_by_email", "send_message"},
		OwnerUserId: "u1", AccountIds: []string{"acc-1"}}}}
}

func (f *fakeConns) ListInstances(_ context.Context, p Principal, key string) ([]*hostpb.ConnectorInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUser = p.User.ID
	var out []*hostpb.ConnectorInstance
	for _, i := range f.instances {
		if key == "" || i.Connector == key {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *fakeConns) Op(key, op string) (bool, bool) {
	if key != "slack" {
		return false, false
	}
	switch op {
	case "lookup_user_by_email":
		return false, true
	case "send_message":
		return true, true
	}
	return false, false
}

func (f *fakeConns) Execute(_ context.Context, p Principal, c ConnectorCall) (*ConnectorResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	f.lastUser, f.lastTags = p.User.ID, p.TagIDs
	return &ConnectorResult{RunID: "run-1", Status: "success", ResultJSON: `{"ok":true,"token":"wick_enc_masked"}`}, nil
}

// newTestRouter stands in for wick's router: it echoes the identity and
// headers the request reached it with.
func newTestRouter() http.Handler {
	mux := http.NewServeMux()
	echo := func(w http.ResponseWriter, r *http.Request) {
		uid := ""
		if u := login.GetUser(r.Context()); u != nil {
			uid = u.ID
		}
		http.SetCookie(w, &http.Cookie{Name: "sess", Value: "x"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": uid, "tags": login.GetUserTagIDs(r.Context()), "path": r.URL.Path, "query": r.URL.RawQuery,
			"authorization": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"),
			"x_wick": r.Header.Get("X-Wick-User-Id"), "forwarded": r.Header.Get("X-Forwarded-For"), "custom": r.Header.Get("X-Custom"),
		})
	}
	mux.HandleFunc("GET /api/tickets/{id}", echo)
	mux.HandleFunc("GET /api/things/", echo)
	mux.HandleFunc("POST /api/tickets", func(w http.ResponseWriter, r *http.Request) {
		if login.GetUser(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /api/redirect/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	return mux
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // one :memory: database
	for _, q := range []string{
		"CREATE TABLE tickets (id INTEGER PRIMARY KEY, title TEXT)",
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)",
		"CREATE TABLE secrets (id INTEGER PRIMARY KEY, value TEXT)",
		"INSERT INTO tickets (id, title) VALUES (1, 'a'), (2, NULL), (3, 'c')",
		"INSERT INTO notes (id, body) VALUES (1, 'n1'), (2, 'n2')",
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func auditRows(f *fixture) []entity.PluginHostCall {
	f.aud.Start(context.Background())
	f.aud.Close()
	return f.audit.snapshot()
}

func wantDenied(t *testing.T, err error, scope, item string) {
	t.Helper()
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || pd.Scope != scope || pd.Item != item {
		t.Fatalf("err = %v, want PermissionDenied %s %q", err, scope, item)
	}
}

func TestExecuteConnector(t *testing.T) {
	f := newFixture(t)
	ctx := as(Caller{PluginKey: "gen", UserID: "u2", Source: SourceCaller})

	_, err := f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "delete_channel"})
	wantDenied(t, err, "connectors.execute", "slack.delete_channel")
	if len(f.conns.calls) != 0 {
		t.Fatal("undeclared op reached the connector")
	}

	out, err := f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "lookup_user_by_email", ArgsJson: []byte(`{"email":"a@b.c","n":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.ResultJson) != `{"ok":true,"token":"wick_enc_masked"}` || out.Meta["instance_id"] != "inst-1" || out.Meta["run_id"] != "run-1" {
		t.Fatalf("out = %+v", out)
	}
	c := f.conns.calls[0]
	if f.conns.lastUser != "u2" || c.InstanceID != "inst-1" || c.Args["email"] != "a@b.c" || c.PluginKey != "gen" || !slices.Equal(f.conns.lastTags, []string{"tag-u2"}) {
		t.Fatalf("ran as %q with %+v", f.conns.lastUser, c)
	}

	// Destructive op: allowed by default, flagged in the audit.
	if _, err := f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "send_message", InstanceId: "inst-1"}); err != nil {
		t.Fatal(err)
	}
	// An admin blocks that one op.
	_ = f.perms.Set(context.Background(), "gen", "connectors.execute", "slack.send_message", false, "admin")
	_, err = f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "send_message"})
	wantDenied(t, err, "connectors.execute", "slack.send_message")

	// Declared but unknown to the module.
	if _, err := f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "ghost"}); status.Code(err) != codes.NotFound {
		t.Fatalf("ghost op err = %v", err)
	}
	// Two visible instances and none named.
	f.conns.instances = append(f.conns.instances, &hostpb.ConnectorInstance{Id: "inst-2", Connector: "slack"})
	if _, err := f.srv.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "lookup_user_by_email"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ambiguous instance err = %v", err)
	}
	// A plugin that did not declare connectors.execute at all.
	_, err = f.srv.ExecuteConnector(as(Caller{PluginKey: "sync", UserID: "u2"}), &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "lookup_user_by_email"})
	wantDenied(t, err, "connectors.execute", "slack.lookup_user_by_email")

	rows := auditRows(f)
	if len(rows) != 7 {
		t.Fatalf("audit rows = %d: %+v", len(rows), rows)
	}
	if r := rows[2]; r.Item != "slack.send_message" || !strings.Contains(r.Note, "destructive") || !strings.Contains(r.Note, "instance=inst-1") || r.UserID != "u2" {
		t.Fatalf("destructive audit row = %+v", r)
	}
}

func TestListConnectorInstances(t *testing.T) {
	f := newFixture(t)
	out, err := f.srv.ListConnectorInstances(as(Caller{PluginKey: "gen", UserID: "u1"}), &hostpb.ListConnectorInstancesRequest{Connector: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Instances) != 1 || out.Instances[0].Id != "inst-1" || out.Instances[0].OwnerUserId != "u1" ||
		!slices.Equal(out.Instances[0].AccountIds, []string{"acc-1"}) || f.conns.lastUser != "u1" {
		t.Fatalf("instances = %+v as %q", out.Instances, f.conns.lastUser)
	}
}

func TestInvokeRegistry(t *testing.T) {
	f := newFixture(t)
	RegisterMethod("test.echo", "users.read", false, func(_ context.Context, in InvokeCall) (json.RawMessage, error) {
		return json.RawMessage(`{"plugin":"` + in.PluginKey + `","user":"` + in.UserID + `","args":` + string(in.Args) + `}`), nil
	})
	RegisterMethod("test.mine", "tickets.read", false, func(_ context.Context, in InvokeCall) (json.RawMessage, error) {
		return json.RawMessage(`"` + in.UserID + `"`), nil
	})
	t.Cleanup(func() { unregisterMethod("test.echo"); unregisterMethod("test.mine") })

	plain := as(Caller{PluginKey: "gen"})
	out, err := f.srv.Invoke(plain, &hostpb.InvokeRequest{Method: "test.echo", ArgsJson: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.ResultJson) != `{"plugin":"gen","user":"","args":{"a":1}}` {
		t.Fatalf("echo = %s", out.ResultJson)
	}
	// tickets.read is user-level: no person, no call; with one, it runs as them.
	if _, err := f.srv.Invoke(plain, &hostpb.InvokeRequest{Method: "test.mine"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("user-level method without a user: %v", err)
	}
	out, err = f.srv.Invoke(as(Caller{PluginKey: "gen", UserID: "u1"}), &hostpb.InvokeRequest{Method: "test.mine"})
	if err != nil || string(out.ResultJson) != `"u1"` {
		t.Fatalf("mine = %v %v", out, err)
	}
	// The scope gates the method; the method name is its admin item.
	_ = f.perms.Set(context.Background(), "gen", "users.read", "test.echo", false, "admin")
	_, err = f.srv.Invoke(plain, &hostpb.InvokeRequest{Method: "test.echo"})
	wantDenied(t, err, "users.read", "test.echo")
	// Built-in alias runs the RPC itself.
	out, err = f.srv.Invoke(plain, &hostpb.InvokeRequest{Method: "users.list", ArgsJson: []byte(`{"q":"bob"}`)})
	if err != nil || !strings.Contains(string(out.ResultJson), "Bob@Example.com") || strings.Contains(string(out.ResultJson), "ada@") {
		t.Fatalf("users.list = %s %v", out.GetResultJson(), err)
	}
	if _, err := f.srv.Invoke(plain, &hostpb.InvokeRequest{Method: "nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown method: %v", err)
	}
	ms, _ := f.srv.ListMethods(plain, &hostpb.ListMethodsRequest{})
	var names []string
	for _, m := range ms.Methods {
		names = append(names, m.Name)
	}
	for _, want := range []string{"Invoke", "users.list", "sessions.list", "test.echo"} {
		if !slices.Contains(names, want) {
			t.Errorf("ListMethods lacks %s: %v", want, names)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("RegisterMethod under a resource scope should panic")
			}
		}()
		RegisterMethod("test.rest", "rest.read", false, func(context.Context, InvokeCall) (json.RawMessage, error) { return nil, nil })
	}()
}

func restBody(t *testing.T, out *hostpb.RestResponse) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(out.Body, &m); err != nil {
		t.Fatalf("body %q: %v", out.Body, err)
	}
	return m
}

func TestRest(t *testing.T) {
	f := newFixture(t)
	ctx := as(Caller{PluginKey: "gen", UserID: "u2", Source: SourceCaller})

	out, err := f.srv.Rest(ctx, &hostpb.RestRequest{Method: "GET", Path: "/api/tickets/7?x=1", Headers: map[string]string{
		"Authorization": "Bearer stolen", "cookie": "s=1", "X-Wick-User-Id": "u1", "X-Forwarded-For": "1.2.3.4", "X-Custom": "kept"}})
	if err != nil {
		t.Fatal(err)
	}
	b := restBody(t, out)
	if out.Status != 200 || b["user"] != "u2" || b["path"] != "/api/tickets/7" || b["query"] != "x=1" || b["custom"] != "kept" {
		t.Fatalf("rest = %d %v", out.Status, b)
	}
	for _, k := range []string{"authorization", "cookie", "x_wick", "forwarded"} {
		if b[k] != "" {
			t.Errorf("header %s reached wick: %v", k, b[k])
		}
	}
	if tags, _ := b["tags"].([]any); len(tags) != 1 || tags[0] != "tag-u2" {
		t.Errorf("tags = %v", b["tags"])
	}
	if _, ok := out.Headers["Set-Cookie"]; ok {
		t.Error("Set-Cookie passed back to the plugin")
	}

	for _, bad := range []string{"/admin/users", "/api/tickets/../x", "/api/tickets/%2e%2e", "/api/tickets/%2E%2e/x", "//evil.example/api/x", "http://evil/api/x", "/api//tickets", "/api/a\\b"} {
		if _, err := f.srv.Rest(ctx, &hostpb.RestRequest{Path: bad}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("path %q: err = %v, want InvalidArgument", bad, err)
		}
	}
	_, err = f.srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/other"})
	wantDenied(t, err, "rest.read", "GET /api/other")
	_, err = f.srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/tickets/1/sessions"})
	wantDenied(t, err, "rest.read", "GET /api/tickets/1/sessions")
	if out, err := f.srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/things/a/b"}); err != nil || out.Status != 200 {
		t.Fatalf("** glob: %v %v", out, err)
	}

	// rest.write is off until an admin allows it.
	_, err = f.srv.Rest(ctx, &hostpb.RestRequest{Method: "POST", Path: "/api/tickets", Body: []byte(`{}`)})
	wantDenied(t, err, "rest.write", "POST /api/tickets")
	allowAll(t, f)
	if out, err := f.srv.Rest(ctx, &hostpb.RestRequest{Method: "POST", Path: "/api/tickets", Body: []byte(`{}`)}); err != nil || out.Status != 201 {
		t.Fatalf("POST after allow: %v %v", out, err)
	}

	// 3xx comes back as-is, not followed.
	out, err = f.srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/redirect/1"})
	if err != nil || out.Status != 302 || out.Headers["Location"] != "/elsewhere" {
		t.Fatalf("redirect = %v %v", out, err)
	}

	// S3: plugin management is out of reach whatever is declared.
	for _, reserved := range []string{"/manager/api/plugins/x/replace", "/manager/api/plugin-sources", "/manager/api/service-plugins"} {
		if _, err := f.srv.Rest(ctx, &hostpb.RestRequest{Method: "POST", Path: reserved}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("reserved %s: err = %v", reserved, err)
		}
	}
	// S4: HEAD carries no body; an oversized request body is refused.
	if out, err := f.srv.Rest(ctx, &hostpb.RestRequest{Method: "HEAD", Path: "/api/tickets/1"}); err != nil || out.Status != 200 || len(out.Body) != 0 {
		t.Fatalf("HEAD = %v %v", out, err)
	}
	if _, err := f.srv.Rest(ctx, &hostpb.RestRequest{Method: "POST", Path: "/api/tickets", Body: make([]byte, RestMaxRequestBytes+1)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("big body err = %v", err)
	}

	// Blocking the declared pattern blocks every path it matches.
	_ = f.perms.Set(context.Background(), "gen", "rest.read", "GET /api/things/**", false, "admin")
	_, err = f.srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/things/z"})
	wantDenied(t, err, "rest.read", "GET /api/things/**")
}

func TestQuery(t *testing.T) {
	f := newFixture(t)
	ctx := as(Caller{PluginKey: "gen", UserID: "u1"})
	_, err := f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT id FROM tickets"})
	wantDenied(t, err, "db.read", "") // off by default
	allowAll(t, f)

	out, err := f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT id, title FROM tickets WHERE id > ? ORDER BY id", Args: []string{"0"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Columns, []string{"id", "title"}) || len(out.Rows) != 3 || out.Truncated ||
		out.Rows[0].Values[1] != "a" || !out.Rows[1].Nulls[1] || out.Rows[2].Values[0] != "3" {
		t.Fatalf("rows = %+v", out)
	}
	out, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT id FROM tickets ORDER BY id", MaxRows: 2})
	if err != nil || len(out.Rows) != 2 || !out.Truncated {
		t.Fatalf("max_rows: %+v %v", out, err)
	}
	if _, err := f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "WITH t AS (SELECT * FROM tickets) SELECT count(*) FROM t"}); err != nil {
		t.Fatalf("cte: %v", err)
	}

	_, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT * FROM tickets JOIN secrets ON secrets.id = tickets.id"})
	wantDenied(t, err, "db.read", "secrets:read")
	_, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "DELETE FROM notes"})
	wantDenied(t, err, "db.write", "")
	if _, err := f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT 1; DELETE FROM notes"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("two statements: %v", err)
	}
	if _, err := f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "DROP TABLE notes", Write: true}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DDL: %v", err)
	}

	out, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "UPDATE notes SET body = 'x' WHERE id = 1", Write: true})
	if err != nil || out.RowsAffected != 1 {
		t.Fatalf("write: %+v %v", out, err)
	}
	_, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "UPDATE tickets SET title = 'x'", Write: true})
	wantDenied(t, err, "db.write", "tickets:write")
	// S1: a write still checks what it reads under db.read.
	_ = f.perms.Set(context.Background(), "gen", "db.read", "tickets:read", false, "admin")
	_, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "INSERT INTO notes (body) SELECT title FROM tickets", Write: true})
	wantDenied(t, err, "db.read", "tickets:read")
	_ = f.perms.Clear(context.Background(), "gen", "db.read", "tickets:read")
	// S2: binary values come back as \x hex.
	out, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT X'00ff' AS b, 'é' AS s"})
	if err != nil || out.Rows[0].Values[0] != `\x00ff` || out.Rows[0].Values[1] != "é" {
		t.Fatalf("binary: %+v %v", out, err)
	}
	_ = f.perms.Set(context.Background(), "gen", "db.write", "notes:write", false, "admin")
	_, err = f.srv.Query(ctx, &hostpb.QueryRequest{Sql: "DELETE FROM notes WHERE id = 2", Write: true})
	wantDenied(t, err, "db.write", "notes:write")
	var n int64
	f.db.Raw("SELECT count(*) FROM notes").Scan(&n)
	if n != 2 {
		t.Fatalf("notes rows = %d, a denied write ran", n)
	}

	rows := auditRows(f)
	var sawSQL bool
	for _, r := range rows {
		if r.RPC == "Query" && strings.Contains(r.Note, "sql: SELECT id, title FROM tickets WHERE id > ? ORDER BY id params: [\"0\"]") {
			sawSQL = true
		}
	}
	if !sawSQL {
		t.Fatalf("audit lacks the SQL: %+v", rows)
	}
}

func TestQueryNoteCapped(t *testing.T) {
	n := queryNote(&hostpb.QueryRequest{Sql: strings.Repeat("é", 5000)})
	if len(n) > queryNoteMax+len("…") || !strings.HasSuffix(n, "…") {
		t.Fatalf("note len %d", len(n))
	}
	n = queryNote(&hostpb.QueryRequest{Sql: "select $1", Args: []string{strings.Repeat("t", 500), "ok"}})
	if want := `sql: select $1 params: ["` + strings.Repeat("t", 64) + `…","ok"]`; n != want {
		t.Fatalf("note = %q", n)
	}
}

func TestScanSQL(t *testing.T) {
	cases := []struct {
		sql           string
		reads, writes string
		write         bool
		err           bool
	}{
		{sql: "select 1"},
		{sql: "SELECT * FROM users u JOIN public.tickets t ON t.uid = u.id, notes", reads: "users,tickets,notes"},
		{sql: "select ';' as x, $$ drop table x; $$ from users -- ; delete from x\n", reads: "users"},
		{sql: "select * from (select id from users) a where exists (select 1 from tickets)", reads: "users,tickets"},
		{sql: `select * from "Users"`, reads: "Users"},
		{sql: "select extract(year from created_at), substring(name from 2) from users", reads: "users"},
		{sql: "select a is distinct from b from users", reads: "users"},
		{sql: "WITH t AS (SELECT * FROM tickets), u AS MATERIALIZED (SELECT * FROM t) SELECT * FROM u", reads: "tickets"},
		// A CTE does not hide the table of the same name inside its own body.
		{sql: "WITH users AS (SELECT * FROM users) SELECT * FROM users", reads: "users"},
		// A CTE of a subquery is not visible outside it.
		{sql: "SELECT * FROM (WITH s AS (SELECT 1) SELECT * FROM s) a, s", reads: "s"},
		// A WINDOW name is no CTE.
		{sql: "SELECT now() FROM t WINDOW w AS (), secrets AS () UNION SELECT * FROM secrets", reads: "t,secrets"},
		{sql: "insert into notes (body) select title from tickets", reads: "tickets", writes: "notes", write: true},
		{sql: "update notes set body = 'x' from tickets where tickets.id = notes.id", reads: "tickets", writes: "notes", write: true},
		{sql: "delete from only notes where id = 1", writes: "notes", write: true},
		{sql: "delete from notes using secrets, t where notes.id = secrets.id", reads: "secrets,t", writes: "notes", write: true},
		{sql: "select * from a join b using (id), c", reads: "a,b,c"},
		{sql: "WITH d AS (DELETE FROM notes RETURNING *) SELECT * FROM d", writes: "notes", write: true},
		{sql: "insert into notes values (1) on conflict (id) do update set body = 'x'", writes: "notes", write: true},
		{sql: "table tickets", reads: "tickets"},
		{sql: "select 1;"},
		{sql: "select 1; select 2", err: true},
		{sql: "select * into copy_t from tickets", err: true},
		{sql: "select pg_read_file('/etc/passwd')", err: true},
		{sql: "select * from dblink('x', 'y')", err: true},
		{sql: "select * from tickets for update", err: true},
		{sql: "drop table x", err: true},
		{sql: "copy x to '/tmp/x'", err: true},
		{sql: "explain analyze delete from x", err: true},
		{sql: "set role postgres", err: true},
		{sql: "select 'unterminated", err: true},
		// B1: nested block comments and \r-terminated line comments.
		{sql: "SELECT 1 /* /* */ ' */ , (SELECT pw FROM users) -- '", reads: "users"},
		{sql: "SELECT 1 --\r, (SELECT pw FROM users)", reads: "users"},
		{sql: "SELECT 1 /* /* */", err: true},
		{sql: "select $q$ from secrets $q$, E'it\\'s from secrets', 'x''y from secrets' from t", reads: "t"},
		// B2: TABLE in any position.
		{sql: "WITH x AS (TABLE secrets) SELECT * FROM x", reads: "secrets"},
		{sql: "SELECT * FROM (TABLE secrets) s", reads: "secrets"},
		{sql: "SELECT 1 UNION TABLE secrets", reads: "secrets"},
		// B3: functions are allowlisted, quoted/qualified/U& calls refused.
		{sql: `SELECT "pg_terminate_backend"(1)`, err: true},
		{sql: `SELECT pg_catalog."pg_read_file"('x')`, err: true},
		{sql: `SELECT pg_catalog.lower('x')`, err: true},
		{sql: `SELECT U&"\0070g_sleep"(1)`, err: true},
		{sql: "select * from ts_stat('select to_tsvector(x) from users')", err: true},
		{sql: "select setval('s', 1)", err: true},
		{sql: "select nextval('s')", err: true},
		{sql: "select pg_advisory_lock(1)", err: true},
		{sql: "select coalesce(a, 'x'), lower(b), count(*), date_trunc('day', c), jsonb_build_object('k', d), cast(e as varchar(10)), f::numeric(10,2) from t", reads: "t"},
		{sql: "select * from generate_series(1, 3) as g(n)"},
		{sql: "WITH c(a, b) AS (SELECT 1, 2) SELECT * FROM c"},
		{sql: "insert into public.notes (body) values ('x')", writes: "notes", write: true},
		{sql: "select x in (1, 2), exists (select 1 from t) from t", reads: "t"},
		{sql: "", err: true},
	}
	for _, c := range cases {
		info, err := scanSQL(c.sql)
		if c.err {
			if err == nil {
				t.Errorf("%q: want an error, got %+v", c.sql, info)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.sql, err)
			continue
		}
		if got := strings.Join(info.reads, ","); got != c.reads {
			t.Errorf("%q: reads = %q, want %q", c.sql, got, c.reads)
		}
		if got := strings.Join(info.writes, ","); got != c.writes || info.write != c.write {
			t.Errorf("%q: writes = %q (%v), want %q (%v)", c.sql, got, info.write, c.writes, c.write)
		}
	}
}

func TestCheckRestPath(t *testing.T) {
	p, q, err := checkRestPath("/manager/api/x/by-email/a%40b.c?y=1&z=%2e")
	if err != nil || p != "/manager/api/x/by-email/a%40b.c" || q != "y=1&z=%2e" {
		t.Fatalf("= %q %q %v", p, q, err)
	}
	for _, bad := range []string{"", "api/x", "/api", "/api/", "/api/x/", "/api/./x", "/api/x%2fy", "/api/x%5cy", "/api/x#f", "/api/x y", "/apix/y"} {
		if _, _, err := checkRestPath(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// TestHTTPRoundTripGeneric drives the W1 client against the HTTP door for
// Invoke, Query, ExecuteConnector and the raw Rest passthrough.
func TestHTTPRoundTripGeneric(t *testing.T) {
	f := newFixture(t)
	allowAll(t, f)
	ts := startDoor(t, f.srv)
	hc := &http.Client{Transport: multiRT{"X-Test-Plugin": "gen", "X-Test-User": "u1"},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	h, err := host.FromHTTP(ts.URL, "tok", hc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inv, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "users.list", ArgsJson: []byte(`{"q":"ada"}`)})
	if err != nil || !strings.Contains(string(inv.ResultJson), "ada@example.com") {
		t.Fatalf("Invoke = %v %v", inv, err)
	}
	RegisterMethod("test.http", "tickets.read", false, func(_ context.Context, in InvokeCall) (json.RawMessage, error) {
		return json.RawMessage(`{"user":"` + in.UserID + `"}`), nil
	})
	t.Cleanup(func() { unregisterMethod("test.http") })
	if inv, err := h.Invoke(ctx, &hostpb.InvokeRequest{Method: "test.http"}); err != nil || string(inv.ResultJson) != `{"user":"u1"}` {
		t.Fatalf("registered Invoke = %v %v", inv, err)
	}
	q, err := h.Query(ctx, &hostpb.QueryRequest{Sql: "SELECT id, title FROM tickets WHERE title IS NULL"})
	if err != nil || len(q.Rows) != 1 || q.Rows[0].Values[0] != "2" || !q.Rows[0].Nulls[1] {
		t.Fatalf("Query = %+v %v", q, err)
	}
	w, err := h.Query(ctx, &hostpb.QueryRequest{Sql: "DELETE FROM notes WHERE id = 2", Write: true})
	if err != nil || w.RowsAffected != 1 {
		t.Fatalf("Query write = %+v %v", w, err)
	}
	ex, err := h.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{Connector: "slack", Op: "lookup_user_by_email", ArgsJson: []byte(`{"email":"x@y"}`)})
	if err != nil || string(ex.ResultJson) != `{"ok":true,"token":"wick_enc_masked"}` || f.conns.lastUser != "u1" {
		t.Fatalf("ExecuteConnector = %v %v", ex, err)
	}
	r, err := h.Rest(ctx, &hostpb.RestRequest{Path: "/api/tickets/9", Headers: map[string]string{"X-Custom": "v"}})
	if err != nil || r.Status != 200 {
		t.Fatalf("Rest = %v %v", r, err)
	}
	if b := restBody(t, r); b["user"] != "u1" || b["authorization"] != "" || b["custom"] != "v" {
		t.Fatalf("Rest body = %v", b)
	}
	r, err = h.Rest(ctx, &hostpb.RestRequest{Path: "/api/redirect/1"})
	if err != nil || r.Status != 302 || r.Headers["Location"] != "/elsewhere" {
		t.Fatalf("Rest redirect = %v %v", r, err)
	}
	_, err = h.Rest(ctx, &hostpb.RestRequest{Path: "/api/nope"})
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || pd.Scope != "rest.read" {
		t.Fatalf("Rest undeclared = %v", err)
	}
}

type multiRT map[string]string

func (m multiRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range m {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestCapWriterOverflow(t *testing.T) {
	w := &capWriter{header: http.Header{}, limit: 4}
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("de")); err == nil || !w.overflow {
		t.Fatal("overflow not detected")
	}
}

func TestRestSlotsPerPlugin(t *testing.T) {
	s := NewServer(Deps{})
	var rel []func()
	for i := 0; i < RestPerPlugin; i++ {
		r, err := s.restSlot(context.Background(), "p")
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.restSlot(ctx, "p"); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("5th slot err = %v", err)
	}
	if r, err := s.restSlot(ctx, "q"); err != nil {
		t.Fatalf("other plugin blocked: %v", err)
	} else {
		r()
	}
	rel[0]()
	if _, err := s.restSlot(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
}
