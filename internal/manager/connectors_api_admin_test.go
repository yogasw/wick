package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// adminAPIModule mirrors apiDetailModule but opts into OAuth + session
// config so the admin-control endpoints have everything to exercise.
func adminAPIModule(key string) connector.Module {
	m := apiDetailModule(key)
	m.AllowSessionConfig = true
	m.OAuth = &connector.OAuthMeta{
		AuthorizeURL: "https://slack.test/authorize",
		DisplayName:  "Slack",
		Scopes:       "chat:write",
	}
	// OAuth needs a per-instance client_id for the Connect button gate.
	m.Configs = append(m.Configs, entity.Config{Key: "client_id", Type: "text"})
	return m
}

func newAdminHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{adminAPIModule("slack")})
	return &Handler{connectors: svc}, svc
}

// pathReq is adminReq plus the {key}/{id} path values most handlers read.
func pathReq(t *testing.T, method, target, key, id string, body any) *http.Request {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := adminReq(t, method, target, raw)
	r.SetPathValue("key", key)
	r.SetPathValue("id", id)
	return r
}

func TestAPISetConnectorRateLimit(t *testing.T) {
	cases := []struct {
		name    string
		rpm     int
		want    int
		wantSvc int
	}{
		{"positive", 120, 120, 120},
		{"zero unlimited", 0, 0, 0},
		{"negative clamped", -5, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc := newAdminHandler(t)
			row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
			req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]int{"rpm": tc.rpm})
			rec := httptest.NewRecorder()
			h.apiSetConnectorRateLimit(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var got map[string]int
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got["rate_limit_rpm"] != tc.want {
				t.Errorf("resp rpm = %d, want %d", got["rate_limit_rpm"], tc.want)
			}
			after, _ := svc.Get(t.Context(), row.ID)
			if after.RateLimitRPM != tc.wantSvc {
				t.Errorf("persisted rpm = %d, want %d", after.RateLimitRPM, tc.wantSvc)
			}
		})
	}
}

func TestAPISetConnectorRateLimitForbidden(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]int{"rpm": 10})
	normal := &entity.User{ID: "u-normal", Role: entity.RoleUser}
	req = req.WithContext(login.WithUser(req.Context(), normal, nil))
	rec := httptest.NewRecorder()
	h.apiSetConnectorRateLimit(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIDuplicateConnector(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, nil)
	rec := httptest.NewRecorder()
	h.apiDuplicateConnector(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["id"] == "" || got["id"] == row.ID {
		t.Fatalf("expected a fresh id, got %q (src %q)", got["id"], row.ID)
	}
	dup, err := svc.Get(t.Context(), got["id"])
	if err != nil {
		t.Fatalf("dup not found: %v", err)
	}
	if dup.Label != "Prod (copy)" {
		t.Errorf("dup label = %q, want Prod (copy)", dup.Label)
	}
}

func TestAPISetConnectorAccessPolicy(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	body := map[string]bool{
		"allow_others_configure":   true,
		"allow_others_connect_sso": true,
		"enable_sso":               true,
		"multi_account":            true,
	}
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, body)
	rec := httptest.NewRecorder()
	h.apiSetConnectorAccessPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	after, _ := svc.Get(t.Context(), row.ID)
	if !after.EnableSSO || !after.MultiAccount || !after.AllowOthersConnectSSO || !after.AllowOthersConfigure {
		t.Errorf("policy not persisted: %+v", after)
	}
}

func TestAPISetConnectorAccessPolicyAdminOnly(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"enable_sso": true})
	normal := &entity.User{ID: "u-normal", Role: entity.RoleUser}
	req = req.WithContext(login.WithUser(req.Context(), normal, nil))
	rec := httptest.NewRecorder()
	h.apiSetConnectorAccessPolicy(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403/404 for non-admin", rec.Code)
	}
}

func TestAPISetConnectorSessionConfig(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"allow_session_config": true})
	rec := httptest.NewRecorder()
	h.apiSetConnectorSessionConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	after, _ := svc.Get(t.Context(), row.ID)
	if !after.AllowSessionConfig {
		t.Errorf("session config not persisted")
	}
}

func TestAPISetConnectorSessionConfigIncapable(t *testing.T) {
	// A module that did NOT opt into AllowSessionConfig → 400.
	svc := newConnectorsSvcForAPI(t, []connector.Module{apiDetailModule("plain")})
	h := &Handler{connectors: svc}
	row, _ := svc.Create(t.Context(), "plain", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "plain", row.ID, map[string]bool{"allow_session_config": true})
	rec := httptest.NewRecorder()
	h.apiSetConnectorSessionConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for incapable connector; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIToggleConnectorOperation(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"enabled": false})
	req.SetPathValue("opKey", "send")
	rec := httptest.NewRecorder()
	h.apiToggleConnectorOperation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	states, _ := svc.OperationStatesFull(t.Context(), row.ID, "slack")
	if states["send"].Enabled {
		t.Errorf("send op should be disabled after toggle")
	}
}

func TestAPIBulkToggleOperations(t *testing.T) {
	cases := []struct {
		name    string
		ops     []string
		enabled bool
		check   []string
	}{
		{"all ops when empty", nil, false, []string{"send", "del"}},
		{"subset", []string{"send"}, false, []string{"send"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc := newAdminHandler(t)
			row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
			body := map[string]any{"enabled": tc.enabled, "ops": tc.ops}
			req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, body)
			rec := httptest.NewRecorder()
			h.apiBulkToggleOperations(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			states, _ := svc.OperationStatesFull(t.Context(), row.ID, "slack")
			for _, k := range tc.check {
				if states[k].Enabled != tc.enabled {
					t.Errorf("op %q enabled = %v, want %v", k, states[k].Enabled, tc.enabled)
				}
			}
		})
	}
}

func TestAPIToggleOperationAdminOnly(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"admin_only": true})
	req.SetPathValue("opKey", "send")
	rec := httptest.NewRecorder()
	h.apiToggleOperationAdminOnly(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	states, _ := svc.OperationStatesFull(t.Context(), row.ID, "slack")
	if !states["send"].AdminOnly {
		t.Errorf("send op should be admin-only after toggle")
	}
}

func TestAPIToggleOperationAdminOnlyForbidden(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"admin_only": true})
	req.SetPathValue("opKey", "send")
	normal := &entity.User{ID: "u-normal", Role: entity.RoleUser}
	req = req.WithContext(login.WithUser(req.Context(), normal, nil))
	rec := httptest.NewRecorder()
	h.apiToggleOperationAdminOnly(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403/404 for non-admin", rec.Code)
	}
}

func seedAccount(t *testing.T, svc *connectors.Service, rowID, wickUserID string) *entity.ConnectorAccount {
	t.Helper()
	if err := svc.SaveAccount(t.Context(), rowID, wickUserID, "ext-1", "tester", "token-x"); err != nil {
		t.Fatalf("save account: %v", err)
	}
	accs, _ := svc.ListAccounts(t.Context(), rowID)
	if len(accs) == 0 {
		t.Fatalf("account not stored")
	}
	return &accs[0]
}

func TestAPIDisconnectAccount(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	acc := seedAccount(t, svc, row.ID, "u-owner")

	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, nil)
	req.SetPathValue("accountID", acc.ID)
	rec := httptest.NewRecorder()
	h.apiDisconnectAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	accs, _ := svc.ListAccounts(t.Context(), row.ID)
	if len(accs) != 0 {
		t.Errorf("account should be gone, got %d", len(accs))
	}
}

func TestAPIDisconnectAccountByOwnUser(t *testing.T) {
	// Non-admin who is NOT the row owner but IS the account's own user.
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	acc := seedAccount(t, svc, row.ID, "u-self")

	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, nil)
	req.SetPathValue("accountID", acc.ID)
	self := &entity.User{ID: "u-self", Role: entity.RoleUser}
	req = req.WithContext(login.WithUser(req.Context(), self, nil))
	rec := httptest.NewRecorder()
	h.apiDisconnectAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (own account); body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISetAccountDisabledOps(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	acc := seedAccount(t, svc, row.ID, "u-owner")

	req := pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string][]string{"disabled_ops": {"del"}})
	req.SetPathValue("accountID", acc.ID)
	rec := httptest.NewRecorder()
	h.apiSetAccountDisabledOps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	after, _ := svc.GetAccount(t.Context(), acc.ID)
	disabled := connectors.AccountDisabledOps(after)
	if !disabled["del"] {
		t.Errorf("del should be disabled for account, got %+v", disabled)
	}
}

func TestAPIDetailExposesAdminControls(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{"client_id": "cid"}, "u-admin")
	if err := svc.SetAccessPolicy(t.Context(), row.ID, false, false, true, false); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	seedAccount(t, svc, row.ID, "u-owner")

	req := pathReq(t, http.MethodGet, "/x", "slack", row.ID, nil)
	rec := httptest.NewRecorder()
	h.apiConnectorDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got connectorDetailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.IsAdmin {
		t.Errorf("expected is_admin true")
	}
	if !got.SessionConfigCapable {
		t.Errorf("expected session_config_capable true")
	}
	if !got.EnableSSO {
		t.Errorf("expected enable_sso true")
	}
	if got.OAuth == nil || got.OAuth.DisplayName != "Slack" {
		t.Fatalf("oauth meta wrong: %+v", got.OAuth)
	}
	if got.OAuth.StartURL == "" {
		t.Errorf("expected oauth start_url (client_id set + SSO enabled + admin)")
	}
	if len(got.Accounts) != 1 || got.Accounts[0].DisplayName != "tester" {
		t.Fatalf("accounts wrong: %+v", got.Accounts)
	}
	if !got.Accounts[0].CanManage {
		t.Errorf("admin should be able to manage the account")
	}
}
