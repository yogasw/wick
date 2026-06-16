package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// apiDetailModule builds a connector module with a representative config
// schema (required text, secret, hidden, and kvlist fields) so the detail
// endpoint's projection can be asserted.
func apiDetailModule(key string) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         key,
			Name:        "Slack",
			Description: "Slack connector",
			Icon:        "💬",
			DefaultTags: []tool.DefaultTag{tags.Connector, tags.Communication},
		},
		Configs: []entity.Config{
			{Key: "api_url", Type: "url", Required: true, Description: "base url"},
			{Key: "token", Type: "text", IsSecret: true},
			{Key: "internal", Type: "text", Hidden: true},
			{Key: "channels", Type: "kvlist", Options: "id|name"},
		},
		Operations: []connector.Operation{
			{Key: "send", Name: "Send", Description: "Send a message", Execute: noopExec},
			{Key: "del", Name: "Delete", Description: "Delete a message", Destructive: true, Execute: noopExec},
		},
	}
}

func noopExec(*connector.Ctx) (any, error) { return nil, nil }

func adminReq(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	return r.WithContext(login.WithUser(r.Context(), admin, nil))
}

func newDetailHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{apiDetailModule("slack")})
	return &Handler{connectors: svc}, svc
}

func TestAPIConnectorRows(t *testing.T) {
	h, svc := newDetailHandler(t)
	if _, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin"); err != nil {
		t.Fatalf("create row: %v", err)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack", nil)
	req.SetPathValue("key", "slack")
	rec := httptest.NewRecorder()
	h.apiConnectorRows(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got connectorListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Key != "slack" || got.Name != "Slack" || got.OpCount != 2 {
		t.Errorf("metadata wrong: %+v", got)
	}
	if got.MCP {
		t.Errorf("built-in connector should report mcp=false; got %+v", got)
	}
	if len(got.Rows) != 1 || got.Rows[0].Label != "Prod" {
		t.Fatalf("rows wrong: %+v", got.Rows)
	}
	if got.Rows[0].Status != "needs_setup" {
		t.Errorf("status = %q, want needs_setup (required api_url unset)", got.Rows[0].Status)
	}
}

func TestAPIConnectorRowsUnknownKey(t *testing.T) {
	h, _ := newDetailHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/nope", nil)
	req.SetPathValue("key", "nope")
	rec := httptest.NewRecorder()
	h.apiConnectorRows(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIConnectorDetail(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{"token": "xoxb-secret"}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID, nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got connectorDetailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != row.ID || got.Label != "Prod" {
		t.Errorf("identity wrong: %+v", got)
	}
	if !got.CanConfigure {
		t.Errorf("admin should be able to configure")
	}

	byKey := map[string]configFieldJSON{}
	for _, f := range got.Fields {
		byKey[f.Key] = f
	}
	if _, ok := byKey["internal"]; ok {
		t.Errorf("hidden config should not be serialized: %+v", got.Fields)
	}
	if f, ok := byKey["api_url"]; !ok || !f.Required || f.Type != "url" {
		t.Errorf("api_url field wrong: %+v", f)
	}
	tok, ok := byKey["token"]
	if !ok {
		t.Fatalf("token field missing")
	}
	if tok.Value != "" {
		t.Errorf("secret value must be blanked, got %q", tok.Value)
	}
	if !tok.HasValue {
		t.Errorf("token has_value should be true (stored secret)")
	}
	if f, ok := byKey["channels"]; !ok || f.Type != "kvlist" || f.Options != "id|name" {
		t.Errorf("channels kvlist field wrong: %+v", f)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("ops len = %d, want 2", len(got.Operations))
	}
	for _, op := range got.Operations {
		if op.Key == "del" && !op.Destructive {
			t.Errorf("del op should be destructive")
		}
	}
}

func TestAPIConnectorDetailNotFound(t *testing.T) {
	h, _ := newDetailHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/missing", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.apiConnectorDetail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPISetConnectorConfig(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")

	body, _ := json.Marshal(map[string]string{"value": "https://slack.test"})
	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/"+row.ID+"/configs/api_url", body)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	req.SetPathValue("configKey", "api_url")
	rec := httptest.NewRecorder()
	h.apiSetConnectorConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	stored := svc.LoadConfigs(*row)
	if stored["api_url"] != "https://slack.test" {
		t.Errorf("config not persisted: %+v", stored)
	}
}

func TestAPISetConnectorLabel(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Old", map[string]string{}, "u-admin")

	body, _ := json.Marshal(map[string]string{"label": "New name"})
	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/"+row.ID+"/label", body)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiSetConnectorLabel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got, _ := svc.Get(t.Context(), row.ID)
	if got.Label != "New name" {
		t.Errorf("label = %q, want New name", got.Label)
	}
}

func TestAPISetConnectorLabelEmpty(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Old", map[string]string{}, "u-admin")
	body, _ := json.Marshal(map[string]string{"label": "   "})
	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/"+row.ID+"/label", body)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiSetConnectorLabel(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAPIToggleConnectorDisabled(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")

	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/"+row.ID+"/disable", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiToggleConnectorDisabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got["disabled"] {
		t.Errorf("expected disabled=true after toggle")
	}
	after, _ := svc.Get(t.Context(), row.ID)
	if !after.Disabled {
		t.Errorf("row not persisted disabled")
	}
}

func TestAPIDeleteConnectorRow(t *testing.T) {
	h, svc := newDetailHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")

	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/"+row.ID+"/delete", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiDeleteConnectorRow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if _, err := svc.Get(t.Context(), row.ID); err == nil {
		t.Errorf("row should be deleted")
	}
}

func TestAPICreateConnectorRow(t *testing.T) {
	h, svc := newDetailHandler(t)

	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/new", nil)
	req.SetPathValue("key", "slack")
	rec := httptest.NewRecorder()
	h.apiCreateConnectorRow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] == "" {
		t.Fatalf("expected new row id, got %+v", got)
	}
	if _, err := svc.Get(t.Context(), got["id"]); err != nil {
		t.Errorf("new row not found: %v", err)
	}
}
