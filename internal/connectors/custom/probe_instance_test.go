package custom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// newProbeFixture stands up an MCP server that only answers for one bearer
// token, plus a wired custom Service with a def + server row on the oauth
// scheme. Returns the service, the instance row id, and the token the
// server accepts.
func newProbeFixture(t *testing.T) (svc *Service, instanceID, goodToken string) {
	t.Helper()
	goodToken = "at-good"

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+goodToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]any{"name": "fake", "version": "1"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{"name": "list_workflows", "description": "List."}},
				},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	db := newTestDB(t)
	connSvc := connectors.NewServiceFromDB(db)
	keys := &fakeKeyStore{vals: map[string]string{}}
	svc = New(Deps{DB: db, Connectors: connSvc, Keys: keys})

	ctx := context.Background()
	srvRow := &entity.CustomConnectorMCPServer{
		Label:       "n8n",
		Transport:   "http",
		URL:         srv.URL + "/mcp",
		AuthScheme:  "oauth",
		AuthHeaders: "[]",
		AuthExtra:   `{"auth_endpoint":"https://as.example.com/authorize","token_endpoint":"https://as.example.com/token","client_id":"cid"}`,
		Headers:     "[]",
	}
	if err := svc.store.CreateServer(ctx, srvRow); err != nil {
		t.Fatalf("create server: %v", err)
	}
	def := &entity.CustomConnector{
		Key:        "n8n_probe",
		Name:       "n8n",
		Source:     entity.CustomConnectorSourceMCP,
		SourceMeta: `{"server_id":"` + srvRow.ID + `"}`,
		Configs:    `[]`,
		Ops:        `[]`,
	}
	if err := svc.store.CreateDef(ctx, def); err != nil {
		t.Fatalf("create def: %v", err)
	}
	svc.mu.Lock()
	svc.keyToID[def.Key] = def.ID
	svc.mu.Unlock()

	row := &entity.Connector{Key: def.Key, Label: "Prod"}
	if err := db.WithContext(ctx).Create(row).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return svc, row.ID, goodToken
}

func setInstanceToken(svc *Service, instanceID, access, refresh, expiry string) {
	owner := "connector:" + instanceID
	ctx := context.Background()
	_ = svc.keys.SetOwned(ctx, owner, cfgOAuthAccess, access)
	_ = svc.keys.SetOwned(ctx, owner, cfgOAuthRefresh, refresh)
	_ = svc.keys.SetOwned(ctx, owner, cfgOAuthExpiry, expiry)
}

// A valid per-instance token proves the account still works.
func TestProbeInstanceOKWithValidToken(t *testing.T) {
	svc, instanceID, good := newProbeFixture(t)
	setInstanceToken(svc, instanceID, good, "", "")

	res, err := svc.ProbeInstance(context.Background(), instanceID, nil)
	if err != nil {
		t.Fatalf("ProbeInstance: %v", err)
	}
	if !res.OK {
		t.Fatalf("OK = false, want true (error=%q)", res.Error)
	}
	if len(res.Tools) != 1 {
		t.Errorf("tools = %d, want 1", len(res.Tools))
	}
}

// A token the server rejects is an auth VERDICT, not a transport error:
// the call must succeed and report OK=false so the row can flag it.
func TestProbeInstanceReportsRejectedToken(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	setInstanceToken(svc, instanceID, "at-revoked", "", "")

	res, err := svc.ProbeInstance(context.Background(), instanceID, nil)
	if err != nil {
		t.Fatalf("ProbeInstance returned a Go error for a refused token: %v", err)
	}
	if res.OK {
		t.Fatal("OK = true for a token the server rejected")
	}
	if res.Error == "" {
		t.Error("Error is empty — the row has nothing to explain the failure")
	}
}

// An instance that never connected must say so plainly rather than
// surfacing a confusing transport error.
func TestProbeInstanceReportsMissingAccount(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)

	res, err := svc.ProbeInstance(context.Background(), instanceID, nil)
	if err != nil {
		t.Fatalf("ProbeInstance: %v", err)
	}
	if res.OK {
		t.Fatal("OK = true for an instance with no connected account")
	}
	if !strings.Contains(res.Error, "no connected account") {
		t.Errorf("Error = %q, want it to name the missing account", res.Error)
	}
}

// Each instance carries its own credentials, so one row's dead token must
// not affect another's verdict — the whole point of moving this per row.
func TestProbeInstanceIsolatesRows(t *testing.T) {
	svc, goodID, good := newProbeFixture(t)
	ctx := context.Background()

	// A second row under the same connector, holding a revoked token.
	bad := &entity.Connector{Key: "n8n_probe", Label: "Staging"}
	row, err := svc.conns.Get(ctx, goodID)
	if err != nil {
		t.Fatalf("get row: %v", err)
	}
	bad.Key = row.Key
	if err := svc.store.db.WithContext(ctx).Create(bad).Error; err != nil {
		t.Fatalf("create second instance: %v", err)
	}

	setInstanceToken(svc, goodID, good, "", "")
	setInstanceToken(svc, bad.ID, "at-revoked", "", "")

	okRes, err := svc.ProbeInstance(ctx, goodID, nil)
	if err != nil {
		t.Fatalf("probe good: %v", err)
	}
	badRes, err := svc.ProbeInstance(ctx, bad.ID, nil)
	if err != nil {
		t.Fatalf("probe bad: %v", err)
	}
	if !okRes.OK {
		t.Errorf("healthy row reported OK=false (%q)", okRes.Error)
	}
	if badRes.OK {
		t.Error("row with a revoked token reported OK=true")
	}
}

// The server row's LastTest columns describe the connector as a whole. One
// instance's expired token must not flip the connector to "Disconnected"
// for every other row.
func TestProbeInstanceLeavesServerStatusAlone(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	ctx := context.Background()
	setInstanceToken(svc, instanceID, "at-revoked", "", "")

	defID, _ := svc.DefIDForKey("n8n_probe")
	def, _ := svc.store.GetDef(ctx, defID)
	serverID := ServerIDForDef(def)

	before, err := svc.store.GetServer(ctx, serverID)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if before.LastTestAt != nil {
		t.Fatal("fixture should start with no recorded test")
	}

	if _, err := svc.ProbeInstance(ctx, instanceID, nil); err != nil {
		t.Fatalf("ProbeInstance: %v", err)
	}

	after, err := svc.store.GetServer(ctx, serverID)
	if err != nil {
		t.Fatalf("get server after: %v", err)
	}
	if after.LastTestAt != nil || after.LastTestOK {
		t.Error("a per-instance probe wrote the shared server status — one bad account would mark the connector down for everyone")
	}
}

// An expired token with no refresh path cannot be renewed; the probe must
// report it rather than silently sending a dead credential.
func TestProbeInstanceExpiredTokenWithoutRefresh(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	setInstanceToken(svc, instanceID, "at-expired", "", past)

	res, err := svc.ProbeInstance(context.Background(), instanceID, nil)
	if err != nil {
		t.Fatalf("ProbeInstance: %v", err)
	}
	if res.OK {
		t.Fatal("OK = true for an expired token with no refresh path")
	}
}
