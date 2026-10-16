package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/agents/clitoken"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

const (
	testSecretA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testSecretB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type ptCall struct {
	method, path, token, secret, body string
	machine                           string
	vals                              map[string]string
	remote                            string
}

func pluginTestCall(t *testing.T, fn http.HandlerFunc, c ptCall) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	if c.remote != "" {
		req.RemoteAddr = c.remote
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.secret != "" {
		req.Header.Set("X-Plugin-Test-Secret", c.secret)
	}
	if c.machine != "" {
		req.Header.Set("X-Plugin-Test-Machine", c.machine)
	}
	for k, v := range c.vals {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func approvePluginTest(t *testing.T, h *Handler, id string) {
	t.Helper()
	approvePluginTestForm(t, h, id, "minutes=30")
}

func approvePluginTestForm(t *testing.T, h *Handler, id, form string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/plugins/dev-mode/"+id+"/approve", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	req.SetPathValue("action", "approve")
	req = req.WithContext(login.WithUser(req.Context(), &entity.User{ID: "u-admin", Role: entity.RoleAdmin}, nil))
	rec := httptest.NewRecorder()
	h.pluginTestDecide(rec, req)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
}

// The approval window comes from the preset select, a custom duration wins
// over it, and both are capped at 8 hours.
func TestPluginTestApproveDuration(t *testing.T) {
	h, _, db := newAdminConnectorsHandler(t)
	cases := []struct {
		form string
		want time.Duration
	}{
		{"minutes=480", 480 * time.Minute},
		{"minutes=30&custom_minutes=300", 300 * time.Minute},
		{"minutes=30&custom_minutes=9999", pluginTestMaxMinutes * time.Minute},
		{"minutes=60&custom_minutes=", 60 * time.Minute},
	}
	for _, c := range cases {
		q := entity.PluginTestRequest{UserID: "u-dev", ConnectorKey: "sso-admin", InstanceID: "i-1", Status: entity.PluginTestPending}
		require.NoError(t, db.Create(&q).Error)
		approvePluginTestForm(t, h, q.ID, c.form)
		require.NoError(t, db.First(&q, "id = ?", q.ID).Error)
		require.NotNil(t, q.ExpiresAt, c.form)
		require.NotNil(t, q.DecidedAt, c.form)
		require.Equal(t, c.want, q.ExpiresAt.Sub(*q.DecidedAt).Round(time.Second), c.form)
	}
}

// The whole gate for a person (PAT): switch, per-request approval, owner-only,
// machine-bound by the client secret, visibility and switch re-checked on use.
func TestPluginTestRequestFlow(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	h.tokens = accesstoken.NewServiceFromDB(db)
	h.auth = login.NewService(db, "")

	require.NoError(t, db.Create(&entity.User{ID: "u-dev", Name: "Dev", Email: "dev@x.test", Role: entity.RoleAdmin, Approved: true}).Error)
	require.NoError(t, db.Create(&entity.User{ID: "u-other", Name: "Other", Email: "o@x.test", Role: entity.RoleAdmin, Approved: true}).Error)
	row, err := svc.Create(ctx, "sso-admin", "Prod Row", map[string]string{}, "u-owner")
	require.NoError(t, err)
	dev, err := h.tokens.Issue(ctx, "u-dev", "cli")
	require.NoError(t, err)
	other, err := h.tokens.Issue(ctx, "u-other", "cli")
	require.NoError(t, err)

	create := `{"connector_key":"sso-admin","instance":"prod row","operation":"ping","host":"laptop","machine":"dev@laptop","client_secret":"` + testSecretA + `"}`
	post := ptCall{method: "POST", path: "/api/plugin-tests", token: dev.Token, body: create, remote: "203.0.113.7:5000"}

	// Off by default.
	code, out := pluginTestCall(t, h.apiPluginTestCreate, post)
	require.Equal(t, http.StatusForbidden, code, out)

	// On: no per-connector list — the request itself is what gets approved.
	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestEnabled, "true"))
	noTok := post
	noTok.token = ""
	code, _ = pluginTestCall(t, h.apiPluginTestCreate, noTok)
	require.Equal(t, http.StatusUnauthorized, code)

	noSecret := post
	noSecret.body = `{"connector_key":"sso-admin","instance":"prod row","machine":"dev@laptop"}`
	code, _ = pluginTestCall(t, h.apiPluginTestCreate, noSecret)
	require.Equal(t, http.StatusBadRequest, code, "a request without a client secret is refused")
	noMachine := post
	noMachine.body = strings.Replace(create, `"machine":"dev@laptop",`, "", 1)
	code, _ = pluginTestCall(t, h.apiPluginTestCreate, noMachine)
	require.Equal(t, http.StatusBadRequest, code, "a request without the runner's machine is refused")

	code, out = pluginTestCall(t, h.apiPluginTestCreate, post)
	require.Equal(t, http.StatusCreated, code, out)
	require.Equal(t, "pending", out["state"])
	require.Equal(t, row.ID, out["instance_id"])
	id := out["id"].(string)

	var q entity.PluginTestRequest
	require.NoError(t, db.First(&q, "id = ?", id).Error)
	require.Equal(t, "203.0.113.7", q.RequestIP, "the IP wick saw is recorded, not the self-reported host")
	require.NotContains(t, q.SecretHash, "aaaa", "only the hash is stored")
	require.Equal(t, "dev@laptop", q.Machine)
	require.True(t, strings.HasPrefix(q.Credential, "pat:"), q.Credential)

	// Same machine again reuses the request; another machine files its own.
	_, again := pluginTestCall(t, h.apiPluginTestCreate, post)
	require.Equal(t, id, again["id"])
	otherMachine := post
	otherMachine.body = strings.Replace(create, testSecretA, testSecretB, 1)
	_, fresh := pluginTestCall(t, h.apiPluginTestCreate, otherMachine)
	require.NotEqual(t, id, fresh["id"])
	otherIP := post
	otherIP.remote = "198.51.100.9:5000"
	_, fresh = pluginTestCall(t, h.apiPluginTestCreate, otherIP)
	require.NotEqual(t, id, fresh["id"], "another network files its own request")
	devToken2, err := h.tokens.Issue(ctx, "u-dev", "cli-2")
	require.NoError(t, err)
	otherTok := post
	otherTok.token = devToken2.Token
	_, fresh = pluginTestCall(t, h.apiPluginTestCreate, otherTok)
	require.NotEqual(t, id, fresh["id"], "another token of the same user files its own request")

	cfg := ptCall{method: "POST", path: "/api/plugin-tests/" + id + "/config", token: dev.Token, secret: testSecretA, machine: "dev@laptop", vals: map[string]string{"id": id}, remote: "203.0.113.7:5000"}
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, cfg)
	require.Equal(t, http.StatusForbidden, code, "pending must not hand out config")

	approvePluginTest(t, h, id)

	code, out = pluginTestCall(t, h.apiPluginTestConfig, cfg)
	require.Equal(t, http.StatusOK, code, out)
	require.Contains(t, out, "configs")

	// Same user, same token, wrong machine: refused.
	wrongMachine := cfg
	wrongMachine.secret = testSecretB
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, wrongMachine)
	require.Equal(t, http.StatusForbidden, code, "approval is bound to the machine that asked")
	wrongHost := cfg
	wrongHost.machine = "dev@other-box"
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, wrongHost)
	require.Equal(t, http.StatusForbidden, code, "machine name must match")
	wrongIP := cfg
	wrongIP.remote = "198.51.100.9:5000"
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, wrongIP)
	require.Equal(t, http.StatusForbidden, code, "IP must match the request")

	// The same user's OTHER token cannot ride on this approval.
	sameUserOtherTok := cfg
	sameUserOtherTok.token = devToken2.Token
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, sameUserOtherTok)
	require.Equal(t, http.StatusForbidden, code, "approval is bound to the token that asked")

	// Someone else's token cannot read it.
	otherUser := cfg
	otherUser.token = other.Token
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, otherUser)
	require.Equal(t, http.StatusNotFound, code)

	require.NoError(t, db.First(&q, "id = ?", id).Error)
	require.Equal(t, 1, q.FetchCount)
	require.Equal(t, "203.0.113.7", q.LastFetchIP)

	// Losing sight of the instance cuts it (disabled rows drop out of
	// everyone's visible list, admins included).
	require.NoError(t, db.Model(&entity.Connector{}).Where("id = ?", row.ID).Update("disabled", true).Error)
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, cfg)
	require.Equal(t, http.StatusForbidden, code, "instance no longer visible after approval")
	require.NoError(t, db.Model(&entity.Connector{}).Where("id = ?", row.ID).Update("disabled", false).Error)

	// Turning the switch off cuts the approved request at once.
	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestEnabled, "false"))
	code, _ = pluginTestCall(t, h.apiPluginTestConfig, cfg)
	require.Equal(t, http.StatusForbidden, code)
}

// An agent session (wick_cli_ token) can file a request but never gets the
// plaintext config, and the token only works from loopback.
func TestPluginTestAgentNeverGetsConfig(t *testing.T) {
	h, svc, db := newAdminConnectorsHandler(t)
	ctx := context.Background()
	h.tokens = accesstoken.NewServiceFromDB(db)
	h.auth = login.NewService(db, "")
	clitoken.SetSecret(func() string { return "test-session-secret-0123456789abcdef" })

	require.NoError(t, db.Create(&entity.User{ID: "u-dev", Name: "Dev", Email: "dev@x.test", Role: entity.RoleAdmin, Approved: true}).Error)
	_, err := svc.Create(ctx, "sso-admin", "Prod Row", map[string]string{}, "u-owner")
	require.NoError(t, err)
	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestEnabled, "true"))
	grant, err := clitoken.Issue("sess-agent", "u-dev", "test", 10*time.Minute)
	require.NoError(t, err)

	create := `{"connector_key":"sso-admin","instance":"prod row","machine":"ubuntu@wick","client_secret":"` + testSecretA + `"}`

	// From another machine the CLI token is refused outright.
	code, _ := pluginTestCall(t, h.apiPluginTestCreate, ptCall{method: "POST", path: "/api/plugin-tests", token: grant.Token, body: create, remote: "203.0.113.7:5000"})
	require.Equal(t, http.StatusUnauthorized, code)

	code, out := pluginTestCall(t, h.apiPluginTestCreate, ptCall{method: "POST", path: "/api/plugin-tests", token: grant.Token, body: create, remote: "127.0.0.1:5000"})
	require.Equal(t, http.StatusCreated, code, out)
	id := out["id"].(string)
	var q entity.PluginTestRequest
	require.NoError(t, db.First(&q, "id = ?", id).Error)
	require.Contains(t, q.Host, "agent session sess-agent", "the admin sees which session asked")

	approvePluginTest(t, h, id)

	code, out = pluginTestCall(t, h.apiPluginTestConfig, ptCall{method: "POST", path: "/api/plugin-tests/" + id + "/config", token: grant.Token, secret: testSecretA, machine: "ubuntu@wick", vals: map[string]string{"id": id}, remote: "127.0.0.1:5000"})
	require.Equal(t, http.StatusForbidden, code, "agents never receive plaintext config")
	require.Contains(t, out["error"], "/run")
}
