package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
)

// seedMCPServer stores a server row plus the MCP definition that backs
// it (SourceMeta carries the server id, matching ServerIDForDef). The
// returned ids let tests hit the JSON prefill and assert the level-1
// gate.
func seedMCPServer(t *testing.T, svc *customconn.Service, createdBy string) (string, string) {
	t.Helper()
	srv := &entity.CustomConnectorMCPServer{
		Label:         "Internal MCP",
		Transport:     "http",
		URL:           "https://mcp.internal.example.com/v1",
		AuthScheme:    "custom_header",
		AuthHeaders:   `[{"key":"X-API-Key","value":"wick_enc_abc","secret":true}]`,
		Headers:       `[{"key":"X-Tenant","value":"acme"}]`,
		ExcludedTools: `["dangerous_tool"]`,
	}
	if err := svc.Store().CreateServer(context.Background(), srv); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	def := &entity.CustomConnector{
		Key:        "internal-mcp",
		Name:       "Internal MCP",
		Icon:       "🔌",
		Source:     entity.CustomConnectorSourceMCP,
		SourceMeta: `{"server_id":"` + srv.ID + `"}`,
		Configs:    `[]`,
		Ops:        `[]`,
		CreatedBy:  createdBy,
	}
	if err := svc.Store().CreateDef(context.Background(), def); err != nil {
		t.Fatalf("seed mcp def: %v", err)
	}
	return srv.ID, def.ID
}

func TestAPIMCPServerForm(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	other := &entity.User{ID: "u-other", Role: entity.RoleUser}

	cases := []struct {
		name     string
		id       func(serverID string) string
		user     *entity.User
		wantCode int
		check    func(t *testing.T, res mcpServerFormResponse)
	}{
		{
			name:     "admin gets the prefilled form",
			id:       func(s string) string { return s },
			user:     admin,
			wantCode: http.StatusOK,
			check: func(t *testing.T, res mcpServerFormResponse) {
				if res.Form == nil {
					t.Fatalf("form nil")
				}
				if res.Form.Label != "Internal MCP" {
					t.Errorf("label = %q, want Internal MCP", res.Form.Label)
				}
				if res.Form.AuthScheme != "custom_header" {
					t.Errorf("auth_scheme = %q, want custom_header", res.Form.AuthScheme)
				}
				if len(res.Form.AuthHeaders) != 1 || res.Form.AuthHeaders[0].Key != "X-API-Key" {
					t.Errorf("auth_headers roundtrip wrong: %+v", res.Form.AuthHeaders)
				}
				if !res.Form.AuthHeaders[0].Secret {
					t.Errorf("auth header secret flag lost")
				}
				if len(res.Form.Headers) != 1 || res.Form.Headers[0].Key != "X-Tenant" {
					t.Errorf("extra headers roundtrip wrong: %+v", res.Form.Headers)
				}
				if len(res.Form.Excluded) != 1 || res.Form.Excluded[0] != "dangerous_tool" {
					t.Errorf("excluded roundtrip wrong: %+v", res.Form.Excluded)
				}
				if res.Form.Icon != "🔌" {
					t.Errorf("icon = %q, want def icon", res.Form.Icon)
				}
				if res.Info == nil || res.Info.DefID == "" {
					t.Errorf("info missing def id: %+v", res.Info)
				}
				if res.Tools == nil {
					t.Errorf("tools = nil, want [] (unreachable server probes empty)")
				}
			},
		},
		{
			name:     "non-creator non-admin gets 404",
			id:       func(s string) string { return s },
			user:     other,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "missing server gets 404",
			id:       func(s string) string { return "does-not-exist" },
			user:     admin,
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc := newCustomHandler(t)
			serverID, _ := seedMCPServer(t, svc, admin.ID)
			id := tc.id(serverID)

			req := reqWithUser(http.MethodGet, "/manager/api/connectors/custom/mcp-servers/edit?id="+id, tc.user)
			rec := httptest.NewRecorder()
			h.apiMCPServerForm(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.check != nil {
				var res mcpServerFormResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
				}
				tc.check(t, res)
			}
		})
	}

	t.Run("404 when service not wired", func(t *testing.T) {
		h := &Handler{}
		req := reqWithUser(http.MethodGet, "/manager/api/connectors/custom/mcp-servers/edit?id=x", admin)
		rec := httptest.NewRecorder()
		h.apiMCPServerForm(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
