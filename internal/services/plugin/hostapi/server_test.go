package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

type fakePlugins map[string]PluginInfo

func (f fakePlugins) Plugin(key string) (PluginInfo, bool) { p, ok := f[key]; return p, ok }

type fakeUsers struct {
	calls atomic.Int32
	users []*entity.User
}

func (f *fakeUsers) ListUsers(context.Context) ([]*entity.User, error) {
	f.calls.Add(1)
	return f.users, nil
}

type fakeConfigs map[string]string // owner + "/" + key

func (f fakeConfigs) Config(_ context.Context, owner, key string) (string, bool, bool, error) {
	v, ok := f[owner+"/"+key]
	return v, ok, key == PATConfigKey || strings.HasSuffix(key, "_secret"), nil
}

type fakePATs map[string]string

func (f fakePATs) Authenticate(_ context.Context, plain string) (string, error) {
	if uid, ok := f[plain]; ok {
		return uid, nil
	}
	return "", errors.New("invalid")
}

// fakeTickets records who each call ran as.
type fakeTickets struct{ lastUser string }

func (f *fakeTickets) GetTicket(_ context.Context, userID string, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error) {
	f.lastUser = userID
	if in.GetId() != "T-1" {
		return nil, status.Error(codes.NotFound, "ticket not found")
	}
	return &hostpb.Ticket{Id: "T-1", ProjectId: "proj", Title: "Webhook 401", Status: "open", Fields: map[string]string{"k": "v"}}, nil
}
func (f *fakeTickets) ListTickets(_ context.Context, userID string, _ *hostpb.ListTicketsRequest) ([]*hostpb.Ticket, error) {
	f.lastUser = userID
	return nil, nil
}
func (f *fakeTickets) CreateTicket(_ context.Context, userID string, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error) {
	f.lastUser = userID
	return &hostpb.Ticket{Id: "T-2", Title: in.GetTitle()}, nil
}
func (f *fakeTickets) UpdateTicket(_ context.Context, userID string, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error) {
	f.lastUser = userID
	return &hostpb.Ticket{Id: in.GetId()}, nil
}
func (f *fakeTickets) AttachSession(_ context.Context, userID string, _ *hostpb.AttachSessionRequest) error {
	f.lastUser = userID
	return nil
}

type fixture struct {
	srv     *Server
	tickets *fakeTickets
	users   *fakeUsers
	perms   *Permissions
	audit   *memoryAudit
	aud     *Audit
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{tickets: &fakeTickets{}, perms: NewPermissions(NewMemoryPermissionStore()), audit: &memoryAudit{}}
	f.users = &fakeUsers{users: []*entity.User{
		{ID: "u1", Name: "Ada", Email: "ada@example.com", Approved: true, Role: entity.RoleAdmin, PasswordHash: "secret-hash", CreatedAt: time.Unix(0, 0)},
		{ID: "u2", Name: "Bob", Email: "Bob@Example.com", Approved: true},
		{ID: "u3", Name: "Pending", Email: "p@example.com"},
		{ID: "u-bot", Name: "Bot", Email: "bot@example.com", Approved: true},
	}}
	f.aud = NewAudit(f.audit)
	f.srv = NewServer(Deps{
		Plugins: fakePlugins{
			"sync":   {Key: "sync", Kind: "job", Version: "1.2.0", ConfigOwner: "job:sync", Scopes: []string{"users.read", "tickets.read", "datadir"}},
			"cronpt": {Key: "cronpt", Kind: "job", ConfigOwner: "job:cronpt", Scopes: []string{"tickets.read"}},
			"badpat": {Key: "badpat", Kind: "job", ConfigOwner: "job:badpat"},
			"setup": {Key: "setup", Kind: "tool", ConfigOwner: "tool:setup", Scopes: []string{"config.read_other", "config.read_secret"},
				Resources: &wickplugin.HostResources{Config: []wickplugin.HostConfigResource{{Owner: "job:other", Key: "api_secret"}}}},
		},
		Users: f.users,
		UserTags: func(_ context.Context, ids []string) (map[string][]string, error) {
			out := map[string][]string{}
			for _, id := range ids {
				out[id] = []string{"tag-" + id}
			}
			return out, nil
		},
		Configs: fakeConfigs{"job:cronpt/" + PATConfigKey: "wick_pat_good", "job:badpat/" + PATConfigKey: "wick_pat_revoked",
			"job:other/api_secret": "s3cret", "job:other/other_secret": "nope", "job:other/region": "id"},
		PATs:        fakePATs{"wick_pat_good": "u-bot"},
		Tickets:     f.tickets,
		Perms:       f.perms,
		Audit:       f.aud,
		WickVersion: "0.9.0",
	})
	return f
}

func as(c Caller) context.Context { return WithCaller(context.Background(), c) }

func TestIdentityCallerPATNone(t *testing.T) {
	f := newFixture(t)
	// 1. The person who triggered the plugin.
	if _, err := f.srv.GetTicket(as(Caller{PluginKey: "sync", UserID: "u1", Source: SourceCaller}), &hostpb.GetTicketRequest{Id: "T-1"}); err != nil {
		t.Fatal(err)
	}
	if f.tickets.lastUser != "u1" {
		t.Fatalf("ran as %q, want the caller u1", f.tickets.lastUser)
	}
	// 2. No caller: the owner of the plugin's wick_pat.
	if _, err := f.srv.GetTicket(as(Caller{PluginKey: "cronpt"}), &hostpb.GetTicketRequest{Id: "T-1"}); err != nil {
		t.Fatal(err)
	}
	if f.tickets.lastUser != "u-bot" {
		t.Fatalf("ran as %q, want the PAT owner", f.tickets.lastUser)
	}
	// 3. Neither: refused, never a made-up identity.
	_, err := f.srv.GetTicket(as(Caller{PluginKey: "sync"}), &hostpb.GetTicketRequest{Id: "T-1"})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), PATConfigKey) {
		t.Fatalf("no identity err = %v, want FailedPrecondition naming %s", err, PATConfigKey)
	}
	// Plugin-level RPCs need no person.
	if _, err := f.srv.ListUsers(as(Caller{PluginKey: "sync"}), &hostpb.ListUsersRequest{}); err != nil {
		t.Fatalf("ListUsers without a user: %v", err)
	}
	// No plugin on the context at all.
	if _, err := f.srv.Whoami(context.Background(), &hostpb.WhoamiRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no caller err = %v", err)
	}
}

func TestUsersCache(t *testing.T) {
	f := newFixture(t)
	now := time.Unix(1000, 0)
	f.srv.now = func() time.Time { return now }
	ctx := as(Caller{PluginKey: "sync"})
	res, err := f.srv.ListUsers(ctx, &hostpb.ListUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Users) != 3 {
		t.Fatalf("approved users = %d, want 3 (unapproved hidden)", len(res.Users))
	}
	if u := res.Users[0]; !u.Admin || u.Tags[0] != "tag-u1" || u.CreatedAt != "1970-01-01T00:00:00Z" {
		t.Fatalf("user = %+v", u)
	}
	if res, _ := f.srv.ListUsers(ctx, &hostpb.ListUsersRequest{IncludeUnapproved: true, Q: "PEND"}); len(res.Users) != 1 {
		t.Fatalf("include_unapproved + q = %d users", len(res.Users))
	}
	if u, err := f.srv.GetUserByEmail(ctx, &hostpb.GetUserByEmailRequest{Email: "bob@example.com"}); err != nil || u.Id != "u2" {
		t.Fatalf("by email = %v, %v", u, err)
	}
	if _, err := f.srv.GetUser(ctx, &hostpb.GetUserRequest{Id: "nobody"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing user err = %v", err)
	}
	if n := f.users.calls.Load(); n != 1 {
		t.Fatalf("directory hit %d times inside the TTL, want 1", n)
	}
	now = now.Add(UsersCacheTTL + time.Second)
	_, _ = f.srv.GetUser(ctx, &hostpb.GetUserRequest{Id: "u1"})
	if n := f.users.calls.Load(); n != 2 {
		t.Fatalf("directory hit %d times after the TTL, want 2", n)
	}
}

func TestGetConfigOwnAndOther(t *testing.T) {
	f := newFixture(t)
	ctx := as(Caller{PluginKey: "cronpt"})
	own, err := f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Key: PATConfigKey})
	if err != nil || own.Value != "" || !own.Found || !own.Secret {
		t.Fatalf("own wick_pat = %+v, %v; wick_pat must never be returned", own, err)
	}
	if pat, _ := f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Owner: "job:cronpt", Key: PATConfigKey}); pat.GetValue() != "" {
		t.Fatal("wick_pat leaked through an explicit owner")
	}
	other, err := f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Owner: "job:other", Key: "api_secret"})
	if err != nil || other.Value != "" || !other.Found || !other.Secret {
		t.Fatalf("other secret = %+v, %v; want found+secret with no value", other, err)
	}
	_ = f.perms.Set(context.Background(), "cronpt", "config.read_other", "", false, "admin")
	if _, err := f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Owner: "job:other", Key: "region"}); !errors.Is(err, host.ErrPermissionDenied) {
		t.Fatalf("blocked read_other err = %v", err)
	}
	if _, err := f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Key: PATConfigKey}); err != nil {
		t.Fatalf("own config must not need config.read_other: %v", err)
	}
}

func TestNotYetRPCsAreUnimplemented(t *testing.T) {
	f := newFixture(t)
	if _, err := f.srv.Query(as(Caller{PluginKey: "sync"}), &hostpb.QueryRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("Query err = %v", err)
	}
}

// startDoor serves HTTPHandler the way the real door will: the plugin is
// identified before the handler runs (here from a test header).
func startDoor(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	h := HTTPHandler(s)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := Caller{PluginKey: "sync", Kind: "job", UserID: r.Header.Get("X-Test-User"), CallbackScopes: nil}
		if c.UserID != "" {
			c.Source = SourceCaller
		}
		h.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), c)))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTPWhoamiSuperset(t *testing.T) {
	f := newFixture(t)
	ts := startDoor(t, f.srv)
	resp, err := http.Get(ts.URL + host.CallbackPrefix + "/api/whoami")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.Header.Get(host.HeaderHostAPI) != "1" {
		t.Fatalf("missing %s header", host.HeaderHostAPI)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"plugin", "scopes", "plugin_key", "plugin_version", "kind", "scopes_declared", "scopes_allowed", "wick_version", "host_api_version"} {
		if _, ok := body[k]; !ok {
			t.Errorf("whoami body lacks %q: %s", k, raw)
		}
	}
	if body["plugin"] != "sync" || body["plugin_key"] != "sync" {
		t.Fatalf("plugin keys = %v / %v", body["plugin"], body["plugin_key"])
	}
	allowed, _ := body["scopes_allowed"].([]any)
	for _, s := range allowed {
		if s == "db.read" || s == "rest.write" {
			t.Errorf("default-off scope %v reported allowed", s)
		}
	}
}

// TestHTTPRoundTripWithClient drives the W1 client (host.FromHTTP) against
// HTTPHandler: the two sides share host.Routes and the codec.
func TestHTTPRoundTripWithClient(t *testing.T) {
	f := newFixture(t)
	ts := startDoor(t, f.srv)
	userHC := &http.Client{Transport: headerRT{"X-Test-User", "u1"}}
	h, err := host.FromHTTP(ts.URL, "tok", userHC)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	users, err := h.ListUsers(ctx, &hostpb.ListUsersRequest{Q: "ad", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(users.Users) != 1 || users.Users[0].Email != "ada@example.com" || users.CachedAt == "" {
		t.Fatalf("ListUsers = %+v", users)
	}

	tk, err := h.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-1"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "Webhook 401" || tk.Fields["k"] != "v" || f.tickets.lastUser != "u1" {
		t.Fatalf("GetTicket = %+v as %q", tk, f.tickets.lastUser)
	}
	if _, err := h.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-404"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing ticket err = %v", err)
	}

	// An admin blocks tickets.read: the client sees a typed PermissionDenied.
	_ = f.perms.Set(ctx, "sync", "tickets.read", "", false, "admin")
	_, err = h.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-1"})
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || pd.Scope != "tickets.read" || pd.Plugin != "sync" {
		t.Fatalf("blocked GetTicket err = %#v", err)
	}
	_ = f.perms.Set(ctx, "sync", "users.read", "", false, "admin")
	if _, err := h.ListUsers(ctx, &hostpb.ListUsersRequest{}); !errors.As(err, &pd) || pd.Scope != "users.read" {
		t.Fatalf("blocked ListUsers err = %v", err)
	}

	// Not served yet -> the SDK's ErrHostAPIUnavailable.
	if _, err := h.Query(ctx, &hostpb.QueryRequest{Sql: "select 1"}); !errors.Is(err, host.ErrHostAPIUnavailable) {
		t.Fatalf("Query err = %v", err)
	}

	// Every call above was audited, the denials as PermissionDenied.
	f.aud.Start(context.Background())
	f.aud.Close()
	calls := f.audit.snapshot()
	var denied int
	for _, c := range calls {
		if c.Code == codes.PermissionDenied.String() {
			denied++
		}
		if c.PluginKey != "sync" {
			t.Errorf("audit row for %q", c.PluginKey)
		}
	}
	if len(calls) != 5 || denied != 2 {
		t.Fatalf("audit rows = %d (denied %d), want 5 (2): %+v", len(calls), denied, calls)
	}
	if calls[1].UserID != "u1" || calls[1].Source != SourceCaller || calls[1].Note != "" {
		t.Fatalf("GetTicket audit row = %+v", calls[1])
	}
}

type headerRT struct{ k, v string }

func (h headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(h.k, h.v)
	return http.DefaultTransport.RoundTrip(r)
}

func TestAuditUndeclaredAndPurge(t *testing.T) {
	f := newFixture(t)
	_, _ = f.srv.ListTickets(as(Caller{PluginKey: "sync", UserID: "u1"}), &hostpb.ListTicketsRequest{ProjectId: "p"})
	_, _ = f.srv.CreateTicket(as(Caller{PluginKey: "sync", UserID: "u1"}), &hostpb.CreateTicketRequest{Title: "x"})
	f.aud.Start(context.Background())
	f.aud.Close()
	calls := f.audit.snapshot()
	if len(calls) != 2 || calls[0].Note != "" || calls[1].Note != "undeclared" {
		t.Fatalf("audit = %+v; want tickets.write noted undeclared", calls)
	}
	old := calls[0]
	old.Ts = time.Now().Add(-AuditRetention - time.Hour)
	_ = f.audit.WriteCalls(context.Background(), []entity.PluginHostCall{old})
	a := NewAudit(f.audit)
	a.Start(context.Background()) // purges on start
	a.Close()
	if n := len(f.audit.snapshot()); n != 2 {
		t.Fatalf("rows after purge = %d, want 2", n)
	}
}
