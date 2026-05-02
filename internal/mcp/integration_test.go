package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
)

// ── helpers ──────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	return db
}

// stubModule is a no-HTTP connector: "echo" returns the "msg" input.
// Used to verify list + execute + run-history without an outbound call.
func stubModule() connector.Module {
	type EchoInput struct {
		Msg string `wick:"desc=Message to echo back"`
	}
	return connector.Module{
		Meta: connector.Meta{
			Key:         "stub",
			Name:        "Stub Connector",
			Description: "In-process test connector",
		},
		Operations: []connector.Operation{
			connector.Op("echo", "Echo", "Returns Msg back to the caller",
				EchoInput{},
				func(c *connector.Ctx) (any, error) {
					return map[string]string{"echo": c.Input("msg")}, nil
				},
			),
		},
	}
}

func newTestService(t *testing.T, db *gorm.DB, mod connector.Module) *connectors.Service {
	t.Helper()
	svc := connectors.NewServiceFromDB(db)
	if err := svc.Bootstrap(context.Background(), []connector.Module{mod}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return svc
}

// localAdminCtx returns the same context RunMCPStdio injects.
func localAdminCtx() context.Context {
	return login.WithUser(
		context.Background(),
		&entity.User{ID: "local", Role: entity.RoleAdmin},
		nil,
	)
}

// dispatchJSON is a shortcut: marshal params to JSON, run dispatchLine,
// unmarshal result into dest. Returns the raw bytes for extra assertions.
func dispatchJSON(t *testing.T, h *Handler, method string, params any) []byte {
	t.Helper()
	p, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, p)
	return h.dispatchLine(localAdminCtx(), []byte(msg))
}

// ── wick_list ────────────────────────────────────────────────────────

func TestWickListShowsBootstrappedConnector(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_list",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content in wick_list response:\n%s", raw)
	}

	var payload listResult
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v\ntext=%s", err, resp.Result.Content[0].Text)
	}
	if payload.TotalConnectors != 1 {
		t.Fatalf("total_connectors = %d, want 1", payload.TotalConnectors)
	}
	if payload.Connectors[0].Connector != "Stub Connector" {
		t.Fatalf("connector name = %q, want %q", payload.Connectors[0].Connector, "Stub Connector")
	}
	if payload.TotalTools != 1 {
		t.Fatalf("total_tools = %d, want 1", payload.TotalTools)
	}
}

// ── wick_execute + run history ───────────────────────────────────────

func TestWickExecuteWritesRunToSQLite(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	// Resolve the connector ID from the DB so we can build tool_id.
	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list connectors: %v (rows=%d)", err, len(rows))
	}
	connectorID := rows[0].ID
	toolID := fmt.Sprintf("conn:%s/echo", connectorID)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name": "wick_execute",
		"arguments": map[string]any{
			"tool_id": toolID,
			"params":  map[string]any{"msg": "hello"},
		},
	})

	// Verify response is success (not isError).
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody=%s", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("execute returned isError=true:\n%s", raw)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "hello") {
		t.Fatalf("unexpected content: %s", raw)
	}

	// Verify a ConnectorRun row was written to SQLite.
	runs, err := svc.ListRuns(context.Background(), connectorID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("connector_runs count = %d, want 1", len(runs))
	}

	run := runs[0]
	if run.Source != entity.ConnectorRunSourceMCP {
		t.Fatalf("run.source = %q, want %q", run.Source, entity.ConnectorRunSourceMCP)
	}
	if run.OperationKey != "echo" {
		t.Fatalf("run.operation_key = %q, want echo", run.OperationKey)
	}
	if run.ConnectorID != connectorID {
		t.Fatalf("run.connector_id = %q, want %q", run.ConnectorID, connectorID)
	}
	if run.UserID != "local" {
		t.Fatalf("run.user_id = %q, want local", run.UserID)
	}
	if run.Status != entity.ConnectorRunStatusSuccess {
		t.Fatalf("run.status = %q, want success", run.Status)
	}
}

func TestWickExecuteViaServeStdioWritesRun(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	rows, _ := svc.List(context.Background())
	connectorID := rows[0].ID
	toolID := fmt.Sprintf("conn:%s/echo", connectorID)

	messages := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wick_execute","arguments":{"tool_id":%q,"params":{"msg":"world"}}}}`, toolID),
	}, "\n")

	var out strings.Builder
	h.ServeStdio(localAdminCtx(), strings.NewReader(messages), &out)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d:\n%s", len(lines), out.String())
	}

	// Second line is the execute result.
	var execResp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &execResp); err != nil {
		t.Fatalf("unmarshal exec response: %v\n%s", err, lines[1])
	}
	if execResp.Result.IsError {
		t.Fatalf("execute via ServeStdio returned isError=true:\n%s", lines[1])
	}

	// Confirm run was persisted.
	runs, _ := svc.ListRuns(context.Background(), connectorID, 10)
	if len(runs) != 1 {
		t.Fatalf("connector_runs count = %d, want 1 after ServeStdio execute", len(runs))
	}
	if runs[0].Source != entity.ConnectorRunSourceMCP {
		t.Fatalf("run.source = %q, want mcp", runs[0].Source)
	}
}

func TestWickExecuteErrorRunStillPersisted(t *testing.T) {
	db := newTestDB(t)
	mod := stubModule()
	// Override echo to always fail.
	mod.Operations[0].Execute = func(c *connector.Ctx) (any, error) {
		return nil, fmt.Errorf("stub error")
	}
	svc := newTestService(t, db, mod)
	h := NewHandler(svc)

	rows, _ := svc.List(context.Background())
	connectorID := rows[0].ID
	toolID := fmt.Sprintf("conn:%s/echo", connectorID)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name": "wick_execute",
		"arguments": map[string]any{
			"tool_id": toolID,
			"params":  map[string]any{},
		},
	})

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if !resp.Result.IsError {
		t.Fatalf("expected isError=true for a failing operation")
	}

	runs, _ := svc.ListRuns(context.Background(), connectorID, 10)
	if len(runs) != 1 {
		t.Fatalf("connector_runs count = %d, want 1 even on error", len(runs))
	}
	if runs[0].Status != entity.ConnectorRunStatusError {
		t.Fatalf("run.status = %q, want error", runs[0].Status)
	}
}
