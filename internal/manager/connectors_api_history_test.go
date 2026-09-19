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

// seedRunAs is seedRun with an explicit actor, so a test can build a run set
// that belongs to more than one person. UserID "" means unattributed — an
// agent or MCP call nobody has to own.
func seedRunAs(t *testing.T, svc *connectors.Service, rowID, actor string, src entity.ConnectorRunSource) {
	t.Helper()
	if _, err := svc.Execute(t.Context(), connectors.ExecuteParams{
		ConnectorID:  rowID,
		OperationKey: "send",
		Input:        map[string]string{"text": "hi"},
		Source:       src,
		UserID:       actor,
		IsAdmin:      actor == "u-admin",
	}); err != nil {
		t.Fatalf("seed run as %q: %v", actor, err)
	}
}

func historyReqAs(t *testing.T, u *entity.User, rowID, query string) *http.Request {
	t.Helper()
	target := "/manager/api/connectors/slack/" + rowID + "/history" + query
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.SetPathValue("key", "slack")
	r.SetPathValue("id", rowID)
	return r.WithContext(login.WithUser(r.Context(), u, nil))
}

func decodeHistory(t *testing.T, h *Handler, req *http.Request) historyJSON {
	t.Helper()
	rec := httptest.NewRecorder()
	h.apiConnectorHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got historyJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func userIDsOf(runs []historyRunJSON) map[string]int {
	out := map[string]int{}
	for _, r := range runs {
		out[r.UserID]++
	}
	return out
}

// "Which identity did this go out as" had no answer on this page: the run
// never recorded the connected account it used, so the User column was the
// only thing to go on — and that is a different question. One person drives
// several accounts, and an agent or MCP client picks one while having no user
// at all.
func TestAPIConnectorHistoryCredentialFilter(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SaveAccount(t.Context(), row.ID, "u-admin", "U123", "yoga.setiawan", "xoxp-test"); err != nil {
		t.Fatalf("save account: %v", err)
	}
	accs, err := svc.ListAccounts(t.Context(), row.ID)
	if err != nil || len(accs) != 1 {
		t.Fatalf("list accounts: %v (%d)", err, len(accs))
	}
	accID := accs[0].ID

	// Two on the row's own credentials, one as the connected account.
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceMCP)
	if _, err := svc.Execute(t.Context(), connectors.ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: "send",
		Input:        map[string]string{"text": "hi"},
		Source:       entity.ConnectorRunSourceTest,
		UserID:       "u-admin",
		IsAdmin:      true,
		AccountID:    accID,
	}); err != nil {
		t.Fatalf("seed account run: %v", err)
	}

	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	// The options come from the accounts CONNECTED to the row, not from the
	// current page — the whole point is to ask about runs you cannot see yet.
	all := decodeHistory(t, h, historyReqAs(t, admin, row.ID, ""))
	want := map[string]string{connectors.RunFilterAccountDefault: "Default credentials", accID: "@yoga.setiawan"}
	if len(all.Credentials) != len(want) {
		t.Fatalf("credential options = %+v, want %d entries", all.Credentials, len(want))
	}
	for _, c := range all.Credentials {
		if want[c.ID] != c.Name {
			t.Errorf("credential option %q = %q, want %q", c.ID, c.Name, want[c.ID])
		}
	}

	t.Run("filter to the row's own credentials", func(t *testing.T) {
		got := decodeHistory(t, h, historyReqAs(t, admin, row.ID, "?credential="+connectors.RunFilterAccountDefault))
		if got.Total != 2 {
			t.Errorf("total = %d, want the 2 runs that used no account", got.Total)
		}
		for _, r := range got.Runs {
			if r.AccountID != "" {
				t.Errorf("run on account %q leaked into the default-credentials filter", r.AccountID)
			}
		}
	})

	t.Run("filter to one connected account", func(t *testing.T) {
		got := decodeHistory(t, h, historyReqAs(t, admin, row.ID, "?credential="+accID))
		if got.Total != 1 {
			t.Fatalf("total = %d, want 1", got.Total)
		}
		if got.Runs[0].AccountID != accID || got.Runs[0].AccountName != "yoga.setiawan" {
			t.Errorf("run should name the account it ran as: %+v", got.Runs[0])
		}
	})

	t.Run("no credential filter returns every run", func(t *testing.T) {
		if all.Total != 3 {
			t.Errorf("total = %d, want 3", all.Total)
		}
	})
}
