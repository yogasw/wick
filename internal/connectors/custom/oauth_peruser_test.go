package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// perUserFixture is a fake MCP resource + authorization server where each
// auth code mints a distinct user's token, so a test can tell whose
// account authenticated a call.
type perUserFixture struct {
	svc     *Service
	conns   *connectors.Service
	mcpURL  string
	mu      sync.Mutex
	callers []string // Authorization header of each tools/call
	listers []string // Authorization header of each tools/list
	refresh []string // refresh_token of each refresh grant
}

func (f *perUserFixture) lastCaller() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.callers) == 0 {
		return ""
	}
	return f.callers[len(f.callers)-1]
}

func newPerUserFixture(t *testing.T) *perUserFixture {
	t.Helper()
	f := &perUserFixture{}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	asURL := srv.URL
	valid := map[string]bool{"Bearer at-A": true, "Bearer at-B": true, "Bearer at-A2": true}

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !valid[auth] {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+asURL+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "helpdesk", "version": "1"}}
		case "tools/list":
			f.mu.Lock()
			f.listers = append(f.listers, auth)
			f.mu.Unlock()
			result = map[string]any{"tools": []map[string]any{{"name": "list_tickets", "description": "List."}}}
		case "tools/call":
			f.mu.Lock()
			f.callers = append(f.callers, auth)
			f.mu.Unlock()
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"resource": asURL + "/mcp", "authorization_servers": []string{asURL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": asURL, "authorization_endpoint": asURL + "/authorize",
			"token_endpoint": asURL + "/token", "registration_endpoint": asURL + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"client_id": "dcr-client"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			user := strings.TrimPrefix(r.PostForm.Get("code"), "code-")
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + user, "refresh_token": "rt-" + user, "expires_in": 3600})
		case "refresh_token":
			f.mu.Lock()
			f.refresh = append(f.refresh, r.PostForm.Get("refresh_token"))
			f.mu.Unlock()
			if r.PostForm.Get("refresh_token") != "rt-A" {
				http.Error(w, "bad refresh", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at-A2", "refresh_token": "rt-A2", "expires_in": 3600})
		default:
			http.Error(w, "bad grant", http.StatusBadRequest)
		}
	})

	db := newTestDB(t)
	f.conns = connectors.NewServiceFromDB(db)
	f.conns.SetConfigs(configs.NewService(db))
	f.svc = New(Deps{DB: db, Connectors: f.conns, Keys: &fakeKeyStore{vals: map[string]string{}}})
	f.mcpURL = srv.URL + "/mcp"
	return f
}

// savePerUser registers the server in per-user (SSO) mode with no Test
// and no login, returning the connector key and first instance id.
func (f *perUserFixture) savePerUser(t *testing.T, label string) (*entity.CustomConnectorMCPServer, string, string) {
	t.Helper()
	form := &ServerForm{Label: label, URL: f.mcpURL, AuthScheme: "oauth", OAuthPerUser: true, OAuthRedirectURI: "http://wick.local/cb"}
	row, key, instanceID, err := f.svc.SaveServer(context.Background(), form, false, "", "admin-1")
	if err != nil {
		t.Fatalf("SaveServer per-user without test: %v", err)
	}
	t.Cleanup(func() { connectors.Unregister(key) })
	if instanceID == "" {
		t.Fatal("per-user save seeded no instance")
	}
	return row, key, instanceID
}

// connect drives one wick user's "Connect my account" popup login.
func (f *perUserFixture) connect(t *testing.T, srv *entity.CustomConnectorMCPServer, instanceID, wickUser, code string) *OAuthLoginResult {
	t.Helper()
	authURL, err := f.svc.StartOAuthAccountLogin(srv, "http://wick.local/cb", instanceID, wickUser, wickUser+" name")
	if err != nil {
		t.Fatalf("start account login: %v", err)
	}
	u, _ := url.Parse(authURL)
	res, err := f.svc.CompleteOAuthLogin(context.Background(), u.Query().Get("state"), code, "http://wick.local/cb")
	if err != nil {
		t.Fatalf("complete account login: %v", err)
	}
	return res
}

func (f *perUserFixture) call(t *testing.T, serverID, instanceID, caller, accountID string) error {
	t.Helper()
	c := connector.NewCtx(context.Background(), instanceID, map[string]string{}, map[string]string{}, http.DefaultClient, nil, nil)
	c.SetCallerUserID(caller)
	c.SetAccountID(accountID)
	_, err := f.svc.executeMCP(c, MCPSource{ServerID: serverID, ToolName: "list_tickets"}, nil)
	return err
}

func TestPerUserSaveWithoutLogin(t *testing.T) {
	f := newPerUserFixture(t)
	srv, key, instanceID := f.savePerUser(t, "Helpdesk PU Save")

	stored, err := f.svc.store.GetServer(context.Background(), srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	meta := parseOAuthMeta(stored.AuthExtra)
	if meta.ClientID != "dcr-client" || meta.TokenEndpoint == "" || meta.AuthEndpoint == "" {
		t.Fatalf("client meta not discovered/registered server-side: %+v", meta)
	}
	row, err := f.conns.Get(context.Background(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if !row.EnableSSO || !row.AllowOthersConnectSSO || !row.MultiAccount {
		t.Errorf("per-user policy not set: sso=%v others=%v multi=%v", row.EnableSSO, row.AllowOthersConnectSSO, row.MultiAccount)
	}
	mod, ok := f.conns.Module(key)
	if !ok || mod.OAuth == nil {
		t.Fatal("oauth MCP module must expose OAuthMeta so the SSO policy card applies")
	}
	if n := len(mod.AllOps()); n != 0 {
		t.Errorf("ops before any account = %d, want 0", n)
	}
	if len(f.listers) != 0 {
		t.Errorf("tools/list ran without an account: %v", f.listers)
	}
	if !f.svc.OAuthPerUser(context.Background(), key) {
		t.Error("OAuthPerUser = false after per-user save")
	}
}

func TestPerUserDiscoveryFailureFailsSave(t *testing.T) {
	f := newPerUserFixture(t)
	form := &ServerForm{Label: "Helpdesk PU Bad", URL: "http://127.0.0.1:1/mcp", AuthScheme: "oauth", OAuthPerUser: true}
	if _, _, _, err := f.svc.SaveServer(context.Background(), form, false, "", "admin-1"); err == nil {
		t.Fatal("save must fail when discovery fails")
	}
	// Non per-user oauth keeps the Test gate.
	form = &ServerForm{Label: "Helpdesk Legacy", URL: f.mcpURL, AuthScheme: "oauth"}
	if _, _, _, err := f.svc.SaveServer(context.Background(), form, false, "", "admin-1"); err == nil || !strings.Contains(err.Error(), "test the connection") {
		t.Fatalf("legacy oauth save without test: err = %v, want the Test gate", err)
	}
}

func TestPerUserTwoUsersEachUseTheirOwnToken(t *testing.T) {
	f := newPerUserFixture(t)
	srv, key, instanceID := f.savePerUser(t, "Helpdesk PU Two")

	resA := f.connect(t, srv, instanceID, "user-a", "code-A")
	if !resA.AsAccount || resA.Key != key {
		t.Fatalf("account connect result = %+v", resA)
	}
	f.connect(t, srv, instanceID, "user-b", "code-B")

	accs, err := f.conns.ListAccounts(context.Background(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 2 {
		t.Fatalf("accounts = %d, want 2", len(accs))
	}
	byUser := map[string]entity.ConnectorAccount{}
	for _, a := range accs {
		byUser[a.WickUserID] = a
	}
	a := byUser["user-a"]
	if a.AccessToken != "at-A" || a.RefreshToken != "wick_enc_rt-A" || a.ExpiresAt == nil {
		t.Errorf("user-a account tokens = %q / %q / %v", a.AccessToken, a.RefreshToken, a.ExpiresAt)
	}
	if a.DisplayName != "user-a name" {
		t.Errorf("display name fallback = %q", a.DisplayName)
	}

	if err := f.call(t, srv.ID, instanceID, "user-a", ""); err != nil {
		t.Fatalf("call as A: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-A" {
		t.Errorf("call as A used %q", got)
	}
	if err := f.call(t, srv.ID, instanceID, "user-b", ""); err != nil {
		t.Fatalf("call as B: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-B" {
		t.Errorf("call as B used %q", got)
	}
	// Explicit @accountId works for the caller's own account…
	if err := f.call(t, srv.ID, instanceID, "user-a", byUser["user-a"].ID); err != nil {
		t.Fatalf("call with explicit own account: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-A" {
		t.Errorf("explicit account call used %q", got)
	}
	// Accounts private (the default): B naming A's account is refused even
	// when the framework gate ran under an admin principal (owner-less
	// spawns use the internal admin token), and A's token is never sent.
	before := f.lastCaller()
	if err := f.call(t, srv.ID, instanceID, "user-b", byUser["user-a"].ID); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
		t.Fatalf("private pool: user-b picking user-a's account err = %v", err)
	}
	if got := f.lastCaller(); got != before {
		t.Errorf("refused call still reached the server with %q", got)
	}
	// With the pool shared on purpose (AllowOthersSeeAccounts — "every user
	// with tag access sees, and can run as, every connected account") B's
	// explicit pick of A's account runs as A.
	if err := f.svc.conns.SetAccessPolicy(context.Background(), instanceID, connectors.AccessPolicy{
		EnableSSO: true, AllowOthersConnectSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, srv.ID, instanceID, "user-b", byUser["user-a"].ID); err != nil {
		t.Fatalf("shared pool: user-b picking user-a's account: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-A" {
		t.Errorf("shared-pool explicit account call used %q", got)
	}
	// A named account that is no longer connected gets its own error, not
	// the "connect your own account" one.
	if err := f.call(t, srv.ID, instanceID, "user-b", "gone-account"); err == nil || !strings.Contains(err.Error(), "no longer connected") {
		t.Fatalf("gone account error = %v", err)
	}

	// The shared op list syncs under the caller's account.
	def := f.svc.defForServer(context.Background(), srv.ID)
	ctxB := login.WithUser(context.Background(), &entity.User{ID: "user-b"}, nil)
	cats, ok := f.svc.liveMCPOps(ctxB, def, instanceID)
	if !ok || len(cats) == 0 {
		t.Fatalf("sync with accounts: ok=%v cats=%d", ok, len(cats))
	}
	if got := f.listers[len(f.listers)-1]; got != "Bearer at-B" {
		t.Errorf("sync as B used %q, want B's token", got)
	}
}

func TestPerUserRefreshPersistsToTheRightAccount(t *testing.T) {
	f := newPerUserFixture(t)
	srv, _, instanceID := f.savePerUser(t, "Helpdesk PU Refresh")
	f.connect(t, srv, instanceID, "user-a", "code-A")
	f.connect(t, srv, instanceID, "user-b", "code-B")

	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	var aID, bID string
	for _, a := range accs {
		if a.WickUserID == "user-a" {
			aID = a.ID
		} else {
			bID = a.ID
		}
	}
	past := time.Now().Add(-time.Hour)
	if err := f.conns.UpdateAccountTokens(context.Background(), aID, "at-A", "", &past); err != nil {
		t.Fatal(err)
	}

	if err := f.call(t, srv.ID, instanceID, "user-a", ""); err != nil {
		t.Fatalf("call as A with expired token: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-A2" {
		t.Errorf("refreshed call used %q, want at-A2", got)
	}
	if len(f.refresh) != 1 || f.refresh[0] != "rt-A" {
		t.Errorf("refresh grants = %v, want one with A's decrypted refresh token", f.refresh)
	}
	a, _ := f.conns.GetAccount(context.Background(), aID)
	if a.AccessToken != "at-A2" || a.RefreshToken != "wick_enc_rt-A2" || a.ExpiresAt == nil || !a.ExpiresAt.After(time.Now()) {
		t.Errorf("A after refresh = %q / %q / %v", a.AccessToken, a.RefreshToken, a.ExpiresAt)
	}
	b, _ := f.conns.GetAccount(context.Background(), bID)
	if b.AccessToken != "at-B" {
		t.Errorf("B was touched by A's refresh: %q", b.AccessToken)
	}
}

func TestPerUserNoAccountIsAHelpfulError(t *testing.T) {
	f := newPerUserFixture(t)
	srv, key, instanceID := f.savePerUser(t, "Helpdesk PU None")
	f.connect(t, srv, instanceID, "user-a", "code-A")

	err := f.call(t, srv.ID, instanceID, "user-c", "")
	if err == nil {
		t.Fatal("call without an own account must fail, never borrow another user's token")
	}
	want := fmt.Sprintf("/manager/connectors/%s/%s", key, instanceID)
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "Connect") {
		t.Errorf("error %q should point at %s", err, want)
	}
	if got := f.lastCaller(); got != "" {
		t.Errorf("a tools/call went out as %q", got)
	}
}

// Legacy per-instance-token instances (Enable SSO off) keep using the
// instance's own token, not accounts.
func TestPerUserLegacyInstanceUnchanged(t *testing.T) {
	f := newPerUserFixture(t)
	srv, key, _ := f.savePerUser(t, "Helpdesk PU Legacy")
	legacy, err := f.conns.Create(context.Background(), key, "Legacy", map[string]string{}, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.EnableSSO {
		t.Fatal("a new instance must not inherit SSO")
	}
	if err := f.svc.persistInstanceTokens(context.Background(), legacy.ID, &oauthTokens{AccessToken: "at-B"}, "shared"); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, srv.ID, legacy.ID, "user-a", ""); err != nil {
		t.Fatalf("legacy call: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-B" {
		t.Errorf("legacy instance call used %q, want the instance token", got)
	}
}

// masterKeyStore mimics the production configs service: EncryptSecret
// returns a wick_cenc_ master token, not wick_enc_.
type masterKeyStore struct{ fakeKeyStore }

func (m *masterKeyStore) EncryptSecret(plain string) (string, error) {
	return "wick_cenc_" + plain, nil
}
func (m *masterKeyStore) DecryptSecret(token string) (string, error) {
	return strings.TrimPrefix(token, "wick_cenc_"), nil
}

// A refresh token stored under the wick_cenc_ master prefix must be
// decrypted before the refresh grant, never POSTed as ciphertext.
func TestPerUserRefreshDecryptsMasterToken(t *testing.T) {
	f := newPerUserFixture(t)
	f.svc.keys = &masterKeyStore{fakeKeyStore{vals: map[string]string{}}}
	srv, _, instanceID := f.savePerUser(t, "Helpdesk PU Master")
	f.connect(t, srv, instanceID, "user-a", "code-A")

	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	if len(accs) != 1 || accs[0].RefreshToken != "wick_cenc_rt-A" {
		t.Fatalf("stored refresh = %+v, want wick_cenc_rt-A", accs)
	}
	past := time.Now().Add(-time.Hour)
	if err := f.conns.UpdateAccountTokens(context.Background(), accs[0].ID, "at-A", "", &past); err != nil {
		t.Fatal(err)
	}
	if err := f.call(t, srv.ID, instanceID, "user-a", ""); err != nil {
		t.Fatalf("call with expired token: %v", err)
	}
	if len(f.refresh) != 1 || f.refresh[0] != "rt-A" {
		t.Errorf("refresh grants = %v, want the decrypted rt-A", f.refresh)
	}
	a, _ := f.conns.GetAccount(context.Background(), accs[0].ID)
	if a.RefreshToken != "wick_cenc_rt-A2" {
		t.Errorf("rotated refresh stored as %q, want wick_cenc_rt-A2 (encrypted once)", a.RefreshToken)
	}
}

// A per-user connect must never replace other users' accounts, even when
// the generic Access policy card cleared MultiAccount.
func TestPerUserConnectForcesMultiAccount(t *testing.T) {
	f := newPerUserFixture(t)
	srv, _, instanceID := f.savePerUser(t, "Helpdesk PU Multi")
	f.connect(t, srv, instanceID, "user-a", "code-A")
	if err := f.conns.SetAccessPolicy(context.Background(), instanceID, connectors.AccessPolicy{
		EnableSSO: true, AllowOthersConnectSSO: true, MultiAccount: false,
	}); err != nil {
		t.Fatal(err)
	}
	f.connect(t, srv, instanceID, "user-b", "code-B")

	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	if len(accs) != 2 {
		t.Fatalf("accounts = %d, want 2 (user-b's connect wiped user-a)", len(accs))
	}
	row, _ := f.conns.Get(context.Background(), instanceID)
	if !row.MultiAccount || !row.EnableSSO || !row.AllowOthersConnectSSO {
		t.Errorf("policy after connect: multi=%v sso=%v others=%v", row.MultiAccount, row.EnableSSO, row.AllowOthersConnectSSO)
	}
}

// Flipping Enable SSO on a legacy per-instance-token instance does not
// divert it to per-user mode: only the per-user marker does.
func TestPerUserLegacyInstanceSSOToggleKeepsToken(t *testing.T) {
	f := newPerUserFixture(t)
	srv, key, _ := f.savePerUser(t, "Helpdesk PU Toggle")
	legacy, err := f.conns.Create(context.Background(), key, "Legacy", map[string]string{}, "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.persistInstanceTokens(context.Background(), legacy.ID, &oauthTokens{AccessToken: "at-B"}, "shared"); err != nil {
		t.Fatal(err)
	}
	if err := f.conns.SetAccessPolicy(context.Background(), legacy.ID, connectors.AccessPolicy{EnableSSO: true}); err != nil {
		t.Fatal(err)
	}
	row, _ := f.conns.Get(context.Background(), legacy.ID)
	if f.svc.InstancePerUser(*row) {
		t.Fatal("Enable SSO alone must not make a legacy instance per-user")
	}
	if err := f.call(t, srv.ID, legacy.ID, "user-a", ""); err != nil {
		t.Fatalf("legacy call with SSO on: %v", err)
	}
	if got := f.lastCaller(); got != "Bearer at-B" {
		t.Errorf("legacy instance call used %q, want the instance token", got)
	}
}
