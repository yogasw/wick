package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// tokenFixture is a fake MCP server gated by an API-key header or a
// bearer token, recording which credential each request carried so a
// test can tell whose value authenticated a call.
type tokenFixture struct {
	svc     *Service
	conns   *connectors.Service
	keys    *fakeKeyStore
	mcpURL  string
	mu      sync.Mutex
	callers []string // credential of each tools/call
	listers []string // credential of each tools/list
	shared  []string // X-Client header of each tools/call
}

func (f *tokenFixture) lastCaller() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.callers) == 0 {
		return ""
	}
	return f.callers[len(f.callers)-1]
}

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	f := &tokenFixture{}
	valid := map[string]bool{"key-a": true, "key-b": true, "Bearer tok-a": true, "Bearer tok-b": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred := r.Header.Get("X-Api-Key")
		if cred == "" {
			cred = r.Header.Get("Authorization")
		}
		if !valid[cred] {
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
			result = map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "mocker", "version": "1"}}
		case "tools/list":
			f.mu.Lock()
			f.listers = append(f.listers, cred)
			f.mu.Unlock()
			result = map[string]any{"tools": []map[string]any{{"name": "list_mocks", "description": "List."}}}
		case "tools/call":
			f.mu.Lock()
			f.callers = append(f.callers, cred)
			f.shared = append(f.shared, r.Header.Get("X-Client"))
			f.mu.Unlock()
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)

	db := newTestDB(t)
	f.conns = connectors.NewServiceFromDB(db)
	f.conns.SetConfigs(configs.NewService(db))
	f.keys = &fakeKeyStore{vals: map[string]string{}}
	f.svc = New(Deps{DB: db, Connectors: f.conns, Keys: f.keys})
	f.mcpURL = srv.URL + "/mcp"
	return f
}

func (f *tokenFixture) save(t *testing.T, form *ServerForm, tested bool) (*entity.CustomConnectorMCPServer, string, string) {
	t.Helper()
	form.URL = f.mcpURL
	row, key, instanceID, err := f.svc.SaveServer(context.Background(), form, tested, "", "admin-1")
	if err != nil {
		t.Fatalf("SaveServer: %v", err)
	}
	t.Cleanup(func() { connectors.Unregister(key) })
	return row, key, instanceID
}

func (f *tokenFixture) call(t *testing.T, serverID, instanceID, caller string, cfg map[string]string) error {
	t.Helper()
	if cfg == nil {
		cfg = map[string]string{}
	}
	c := connector.NewCtx(context.Background(), instanceID, cfg, map[string]string{}, http.DefaultClient, nil, nil)
	c.SetCallerUserID(caller)
	_, err := f.svc.executeMCP(c, MCPSource{ServerID: serverID, ToolName: "list_mocks"}, nil)
	return err
}

func headerForm(label string, perUser bool) *ServerForm {
	return &ServerForm{
		Label:      label,
		AuthScheme: "custom_header",
		AuthHeaders: []HeaderRow{
			{Key: "X-Client", Value: "client-1"},
			{Key: "X-Api-Key", Secret: true, Config: true},
		},
		OAuthPerUser: perUser,
	}
}

func TestUserTokenFieldsPicksConfigRowsFirst(t *testing.T) {
	cases := []struct {
		name string
		rows []HeaderRow
		want []string
	}{
		{"config rows", []HeaderRow{{Key: "A", Secret: true}, {Key: "B", Config: true}}, []string{"B"}},
		{"secret rows", []HeaderRow{{Key: "A"}, {Key: "B", Secret: true}}, []string{"B"}},
		{"every row", []HeaderRow{{Key: "A"}, {Key: "B"}, {Key: ""}}, []string{"A", "B"}},
	}
	for _, tc := range cases {
		var got []string
		for _, fl := range userHeaderFields(tc.rows) {
			got = append(got, fl.Key)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: fields = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Per-user custom header: each user saves their own key, calls run with
// it, the shared header rides along, and nobody borrows another's key.
func TestUserTokenEachUserUsesTheirOwnHeader(t *testing.T) {
	f := newTokenFixture(t)
	srv, key, instanceID := f.save(t, headerForm("Mocker PU Header", true), false)
	if instanceID == "" {
		t.Fatal("per-user save seeded no instance")
	}
	mod, ok := f.conns.Module(key)
	if !ok || mod.OAuth == nil {
		t.Fatal("per-user token module must expose OAuthMeta so SSO accounts apply")
	}
	if !f.svc.UsesUserToken(context.Background(), key) {
		t.Error("UsesUserToken = false for a per-user header server")
	}
	stored, _ := f.svc.store.GetServer(context.Background(), srv.ID)
	if strings.Contains(stored.AuthHeaders, "key-") {
		t.Errorf("server row holds a user value: %s", stored.AuthHeaders)
	}

	if err := f.svc.SaveUserToken(context.Background(), instanceID, "user-a", "User A", map[string]string{"X-Api-Key": "wrong"}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wrong key err = %v, want refused", err)
	}
	if err := f.svc.SaveUserToken(context.Background(), instanceID, "user-a", "User A", map[string]string{}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty values err = %v, want required", err)
	}
	for user, v := range map[string]string{"user-a": "key-a", "user-b": "key-b"} {
		if err := f.svc.SaveUserToken(context.Background(), instanceID, user, user, map[string]string{"X-Api-Key": v}); err != nil {
			t.Fatalf("save %s token: %v", user, err)
		}
	}
	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	if len(accs) != 2 {
		t.Fatalf("accounts = %d, want 2", len(accs))
	}
	for _, a := range accs {
		if !strings.HasPrefix(a.AccessToken, "wick_enc_") {
			t.Errorf("account %s token stored in plaintext: %q", a.WickUserID, a.AccessToken)
		}
	}

	for user, want := range map[string]string{"user-a": "key-a", "user-b": "key-b"} {
		if err := f.call(t, srv.ID, instanceID, user, nil); err != nil {
			t.Fatalf("call as %s: %v", user, err)
		}
		if got := f.lastCaller(); got != want {
			t.Errorf("call as %s used %q, want %q", user, got, want)
		}
	}
	if got := f.shared[len(f.shared)-1]; got != "client-1" {
		t.Errorf("shared header = %q, want client-1", got)
	}

	before := f.lastCaller()
	err := f.call(t, srv.ID, instanceID, "user-c", nil)
	want := fmt.Sprintf("/manager/connectors/%s/%s", key, instanceID)
	if err == nil || !strings.Contains(err.Error(), "token") || !strings.Contains(err.Error(), want) {
		t.Fatalf("no own token err = %v, want a pointer to %s", err, want)
	}
	if got := f.lastCaller(); got != before {
		t.Errorf("call without own token reached the server as %q", got)
	}

	// Re-saving replaces the user's account rather than adding one.
	if err := f.svc.SaveUserToken(context.Background(), instanceID, "user-a", "User A", map[string]string{"X-Api-Key": "key-b"}); err != nil {
		t.Fatal(err)
	}
	if accs, _ := f.conns.ListAccounts(context.Background(), instanceID); len(accs) != 2 {
		t.Errorf("accounts after re-save = %d, want 2", len(accs))
	}
}

// Per-user bearer: a tested admin token becomes the admin's own account
// and never the server row's shared secret.
func TestUserTokenBearerAdminTokenIsTheirAccount(t *testing.T) {
	f := newTokenFixture(t)
	form := &ServerForm{Label: "Mocker PU Bearer", AuthScheme: "bearer", AuthSecret: "tok-a", OAuthPerUser: true}
	srv, key, instanceID := f.save(t, form, true)

	stored, _ := f.svc.store.GetServer(context.Background(), srv.ID)
	if stored.AuthSecret != "" {
		t.Errorf("server row kept the admin token: %q", stored.AuthSecret)
	}
	accs, _ := f.conns.ListAccounts(context.Background(), instanceID)
	if len(accs) != 1 || accs[0].WickUserID != "admin-1" {
		t.Fatalf("accounts = %+v, want the admin's own", accs)
	}
	if mod, _ := f.conns.Module(key); len(mod.AllOps()) == 0 {
		t.Error("ops did not sync under the admin's token")
	}
	if err := f.call(t, srv.ID, instanceID, "admin-1", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.lastCaller(); got != "Bearer tok-a" {
		t.Errorf("admin call used %q", got)
	}
	if err := f.call(t, srv.ID, instanceID, "user-b", nil); err == nil {
		t.Error("a user without a token borrowed the admin's")
	}

	// Bearer without a token still needs one outside per-user mode.
	bad := &ServerForm{Label: "Mocker Bearer Bad", URL: f.mcpURL, AuthScheme: "bearer"}
	if _, _, _, err := f.svc.SaveServer(context.Background(), bad, true, "", "admin-1"); err == nil {
		t.Error("shared bearer without a token saved")
	}
}

// Config headers (per-user off): the value is an instance config, each
// instance runs with its own and the save needs no Test.
func TestConfigHeaderPerInstance(t *testing.T) {
	f := newTokenFixture(t)
	srv, key, instanceID := f.save(t, headerForm("Mocker Config", false), false)
	if instanceID != "" {
		t.Errorf("untested save created instance %s", instanceID)
	}
	mod, _ := f.conns.Module(key)
	var cfgKey string
	for _, c := range mod.Configs {
		if c.Key == headerCfgKey("X-Api-Key") {
			cfgKey = c.Key
			if !c.Required || !c.IsSecret {
				t.Errorf("config header field = %+v, want required + secret", c)
			}
		}
	}
	if cfgKey == "" {
		t.Fatalf("module configs carry no field for the Config header: %+v", mod.Configs)
	}
	if mod.OAuth != nil {
		t.Error("per-instance module must not expose per-user accounts")
	}

	// Two instances, two accounts.
	instA, _ := f.conns.Create(context.Background(), key, "A", map[string]string{}, "admin-1")
	instB, _ := f.conns.Create(context.Background(), key, "B", map[string]string{}, "admin-1")
	for inst, v := range map[string]string{instA.ID: "key-a", instB.ID: "key-b"} {
		if err := f.call(t, srv.ID, inst, "user-x", map[string]string{cfgKey: v}); err != nil {
			t.Fatalf("call on %s: %v", inst, err)
		}
		if got := f.lastCaller(); got != v {
			t.Errorf("instance %s used %q, want %q", inst, got, v)
		}
	}
	err := f.call(t, srv.ID, instA.ID, "user-x", nil)
	if err == nil || !strings.Contains(err.Error(), "X-Api-Key") {
		t.Fatalf("empty instance value err = %v, want the header named", err)
	}

	// Op sync borrows a filled instance, the preferred one first.
	def := f.svc.defForServer(context.Background(), srv.ID)
	if _, ok := f.svc.liveMCPOps(context.Background(), def, ""); ok {
		t.Error("ops synced with no instance filled")
	}
	_ = f.keys.SetOwned(context.Background(), "connector:"+instB.ID, cfgKey, "wick_enc_key-b")
	if cats, ok := f.svc.liveMCPOps(context.Background(), def, instA.ID); !ok || len(cats) == 0 {
		t.Fatalf("sync with a filled instance: ok=%v", ok)
	}
	if got := f.listers[len(f.listers)-1]; got != "key-b" {
		t.Errorf("sync used %q, want instance B's decrypted value", got)
	}
	if res, _ := f.svc.ProbeInstance(context.Background(), instA.ID, nil); res.OK {
		t.Error("probe of an unfilled instance passed")
	}
	if res, _ := f.svc.ProbeInstance(context.Background(), instB.ID, nil); !res.OK {
		t.Errorf("probe of a filled instance failed: %s", res.Error)
	}
}

// A tested Config form lands its value on a new first instance.
func TestConfigHeaderTestedValueSeedsFirstInstance(t *testing.T) {
	f := newTokenFixture(t)
	form := headerForm("Mocker Config Tested", false)
	form.AuthHeaders[1].Value = "key-a"
	srv, _, instanceID := f.save(t, form, true)
	if instanceID == "" {
		t.Fatal("tested Config save created no instance")
	}
	stored, _ := f.svc.store.GetServer(context.Background(), srv.ID)
	if strings.Contains(stored.AuthHeaders, "key-a") {
		t.Errorf("server row holds the instance value: %s", stored.AuthHeaders)
	}
	if got := f.keys.GetOwned("connector:"+instanceID, headerCfgKey("X-Api-Key")); got != "key-a" {
		t.Errorf("first instance value = %q, want key-a", got)
	}
}
