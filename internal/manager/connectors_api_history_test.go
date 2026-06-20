package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// historyModule exposes a single non-destructive op that returns a stable
// payload so seeded runs land as success rows the history projection can
// assert (request/response/op/latency).
func historyModule(key string) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         key,
			Name:        "Slack",
			Icon:        "💬",
			DefaultTags: []tool.DefaultTag{},
		},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Operation{
					Key:     "send",
					Name:    "Send",
					Execute: func(*connector.Ctx) (any, error) { return map[string]string{"ok": "yes"}, nil },
				},
			),
		},
	}
}

func newHistoryHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{historyModule("slack")})
	return &Handler{connectors: svc}, svc
}

// seedRun runs the send op once via the real Execute path so a
// ConnectorRun is logged the same way the panel test logs one.
func seedRun(t *testing.T, svc *connectors.Service, rowID string, src entity.ConnectorRunSource) {
	t.Helper()
	if _, err := svc.Execute(t.Context(), connectors.ExecuteParams{
		ConnectorID:  rowID,
		OperationKey: "send",
		Input:        map[string]string{"text": "hi"},
		Source:       src,
		UserID:       "u-admin",
		IsAdmin:      true,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func TestAPIConnectorHistory(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceMCP)

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/history", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got historyJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 || len(got.Runs) != 2 {
		t.Fatalf("total/runs = %d/%d, want 2/2", got.Total, len(got.Runs))
	}
	if got.Page != 1 || got.TotalPages != 1 || got.PageSize != historyPageSize {
		t.Errorf("pagination wrong: %+v", got)
	}
	if len(got.Ops) != 1 || got.Ops[0].Key != "send" {
		t.Errorf("op options wrong: %+v", got.Ops)
	}
	r0 := got.Runs[0]
	if r0.OperationKey != "send" || r0.Status != string(entity.ConnectorRunStatusSuccess) {
		t.Errorf("run row wrong: %+v", r0)
	}
	if r0.RequestJSON == "" || r0.ResponseJSON == "" {
		t.Errorf("run row should carry request/response JSON: %+v", r0)
	}
	if r0.StartedAt == "" {
		t.Errorf("run row should carry started_at")
	}
}

func TestAPIConnectorHistoryFilterBySource(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceMCP)

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/history?source=mcp", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got historyJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 1 || len(got.Runs) != 1 {
		t.Fatalf("filtered total/runs = %d/%d, want 1/1", got.Total, len(got.Runs))
	}
	if got.Runs[0].Source != string(entity.ConnectorRunSourceMCP) {
		t.Errorf("source filter leaked: %+v", got.Runs[0])
	}
}

func TestAPIConnectorHistoryPagination(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	for i := 0; i < historyPageSize+3; i++ {
		seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/history?page=2", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", row.ID)
	rec := httptest.NewRecorder()
	h.apiConnectorHistory(rec, req)

	var got historyJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != historyPageSize+3 {
		t.Errorf("total = %d, want %d", got.Total, historyPageSize+3)
	}
	if got.TotalPages != 2 || got.Page != 2 {
		t.Errorf("paging = page %d/%d, want page 2/2", got.Page, got.TotalPages)
	}
	if len(got.Runs) != 3 {
		t.Errorf("page 2 runs = %d, want 3 (remainder)", len(got.Runs))
	}
}

func TestAPIConnectorHistoryRowNotFound(t *testing.T) {
	h, _ := newHistoryHandler(t)
	req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/missing/history", nil)
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h.apiConnectorHistory(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
