package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/accesstoken"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

func pluginTestCall(t *testing.T, h *Handler, fn http.HandlerFunc, method, path, pat, body string, pathVals map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if pat != "" {
		req.Header.Set("Authorization", "Bearer "+pat)
	}
	for k, v := range pathVals {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// The whole gate: switch + allowlist + per-request approval, owner-only,
// and switching off cuts an already-approved request.
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

	create := `{"connector_key":"sso-admin","instance":"prod row","operation":"ping","host":"laptop"}`

	// Off by default.
	code, out := pluginTestCall(t, h, h.apiPluginTestCreate, "POST", "/api/plugin-tests", dev.Token, create, nil)
	require.Equal(t, http.StatusForbidden, code, out)

	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestEnabled, "true"))
	code, _ = pluginTestCall(t, h, h.apiPluginTestCreate, "POST", "/api/plugin-tests", dev.Token, create, nil)
	require.Equal(t, http.StatusForbidden, code, "connector not in allowlist yet")

	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestConnectors, "loki,sso-admin"))
	code, _ = pluginTestCall(t, h, h.apiPluginTestCreate, "POST", "/api/plugin-tests", "", create, nil)
	require.Equal(t, http.StatusUnauthorized, code)

	code, out = pluginTestCall(t, h, h.apiPluginTestCreate, "POST", "/api/plugin-tests", dev.Token, create, nil)
	require.Equal(t, http.StatusCreated, code, out)
	require.Equal(t, "pending", out["state"])
	require.Equal(t, row.ID, out["instance_id"])
	id := out["id"].(string)

	// Same user + instance again reuses the pending request.
	_, again := pluginTestCall(t, h, h.apiPluginTestCreate, "POST", "/api/plugin-tests", dev.Token, create, nil)
	require.Equal(t, id, again["id"])

	cfgPath := "/api/plugin-tests/" + id + "/config"
	code, _ = pluginTestCall(t, h, h.apiPluginTestConfig, "POST", cfgPath, dev.Token, "", map[string]string{"id": id})
	require.Equal(t, http.StatusForbidden, code, "pending must not hand out config")

	// Admin approves for 30 minutes.
	areq := httptest.NewRequest("POST", "/admin/plugins/dev-mode/"+id+"/approve", strings.NewReader("minutes=30"))
	areq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	areq.SetPathValue("id", id)
	areq.SetPathValue("action", "approve")
	areq = areq.WithContext(login.WithUser(areq.Context(), &entity.User{ID: "u-admin", Role: entity.RoleAdmin}, nil))
	arec := httptest.NewRecorder()
	h.pluginTestDecide(arec, areq)
	require.Equal(t, http.StatusFound, arec.Code, arec.Body.String())

	code, out = pluginTestCall(t, h, h.apiPluginTestConfig, "POST", cfgPath, dev.Token, "", map[string]string{"id": id})
	require.Equal(t, http.StatusOK, code, out)
	require.Contains(t, out, "configs")

	// Someone else's token cannot read it.
	code, _ = pluginTestCall(t, h, h.apiPluginTestConfig, "POST", cfgPath, other.Token, "", map[string]string{"id": id})
	require.Equal(t, http.StatusNotFound, code)

	var q entity.PluginTestRequest
	require.NoError(t, db.First(&q, "id = ?", id).Error)
	require.Equal(t, 1, q.FetchCount)

	// Losing sight of the instance cuts it too (disabled rows drop out of
	// everyone's visible list, admins included).
	require.NoError(t, db.Model(&entity.Connector{}).Where("id = ?", row.ID).Update("disabled", true).Error)
	code, _ = pluginTestCall(t, h, h.apiPluginTestConfig, "POST", cfgPath, dev.Token, "", map[string]string{"id": id})
	require.Equal(t, http.StatusForbidden, code, "instance no longer visible after approval")
	require.NoError(t, db.Model(&entity.Connector{}).Where("id = ?", row.ID).Update("disabled", false).Error)

	// Turning the switch off cuts the approved request at once.
	require.NoError(t, h.configs.Set(ctx, configs.KeyPluginTestEnabled, "false"))
	code, _ = pluginTestCall(t, h, h.apiPluginTestConfig, "POST", cfgPath, dev.Token, "", map[string]string{"id": id})
	require.Equal(t, http.StatusForbidden, code)
}
