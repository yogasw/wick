// Self-test: exercise every datatable_* MCP op end-to-end through the
// same connector.Ctx surface `wick mcp serve` dispatches into.
package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/pkg/connector"
)

func newSelfTestHandlers() *handlers {
	svc := datatable.NewMock()
	ops := &wfmcp.Ops{DataTables: svc}
	return &handlers{ops: ops}
}

func dispatch(t *testing.T, h func(*connector.Ctx) (any, error), input map[string]string) any {
	t.Helper()
	c := connector.NewCtx(context.Background(), "self-test", nil, input, nil, nil, nil)
	res, err := h(c)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	return res
}

// idEquals compares a JSON-decoded id (float64 after json.Unmarshal)
// against the expected int. Test fixtures may stash ids as either int
// or float once routed through JSON.
func idEquals(got any, want int) bool {
	switch v := got.(type) {
	case int:
		return v == want
	case int64:
		return v == int64(want)
	case float64:
		return v == float64(want)
	}
	return false
}

func dispatchErr(t *testing.T, h func(*connector.Ctx) (any, error), input map[string]string) error {
	t.Helper()
	c := connector.NewCtx(context.Background(), "self-test", nil, input, nil, nil, nil)
	_, err := h(c)
	return err
}

func TestMCP_DataTableLifecycle(t *testing.T) {
	h := newSelfTestHandlers()

	// 1. datatable_list — empty.
	listed := dispatch(t, h.datatableList, map[string]string{})
	if listed == nil {
		t.Fatal("datatable_list returned nil")
	}
	if rows, ok := listed.([]wfmcp.DataTableSummary); !ok || len(rows) != 0 {
		t.Fatalf("datatable_list: want empty []DataTableSummary, got %T %v", listed, listed)
	}

	// 2. datatable_create — register a table.
	created := dispatch(t, h.datatableCreate, map[string]string{
		"slug":        "events",
		"mode":        "strict",
		"primary_key": "id",
		"columns":     "id:string\nstatus:enum\npriority:int",
	})
	m, ok := created.(map[string]any)
	if !ok || m["slug"] != "events" {
		t.Fatalf("datatable_create response: %v", created)
	}

	// 3. datatable_get — schema introspection.
	gotten := dispatch(t, h.datatableGet, map[string]string{"slug": "events"})
	gm, ok := gotten.(map[string]any)
	if !ok || gm["slug"] != "events" {
		t.Fatalf("datatable_get: %v", gotten)
	}
	if rc, _ := gm["row_count"].(int); rc != 0 {
		t.Fatalf("row_count = %d, want 0", rc)
	}

	// 4. datatable_insert + upsert. id is auto-assigned by the store,
	// so the upsert update path is exercised by targeting the
	// auto-stamped id from the first insert (=1).
	dispatch(t, h.datatableInsert, map[string]string{
		"slug": "events",
		"row":  `{"status":"open","priority":1}`,
	})
	upsertRes := dispatch(t, h.datatableUpsert, map[string]string{
		"slug": "events",
		"row":  `{"id":1,"status":"closed","priority":5}`,
	})
	ur := upsertRes.(map[string]any)
	if ur["action"] != "update" {
		t.Fatalf("upsert action = %v, want update", ur["action"])
	}

	// Insert two more for query / count tests.
	dispatch(t, h.datatableInsert, map[string]string{
		"slug": "events",
		"row":  `{"status":"open","priority":9}`,
	})
	dispatch(t, h.datatableInsert, map[string]string{
		"slug": "events",
		"row":  `{"status":"open","priority":3}`,
	})

	// 5. datatable_query — conditions (gte priority 5).
	qres := dispatch(t, h.datatableQuery, map[string]string{
		"slug":       "events",
		"conditions": `[{"column":"priority","op":"gte","value":5}]`,
		"order_by":   `[{"column":"priority","direction":"desc"}]`,
	})
	q := qres.(map[string]any)
	if q["count"] != 2 {
		t.Fatalf("query count = %v, want 2", q["count"])
	}
	rowsRaw, _ := json.Marshal(q["rows"])
	var rows []map[string]any
	_ = json.Unmarshal(rowsRaw, &rows)
	// id is auto-assigned int: priority-9 row got id=2, priority-5 row got id=1.
	// Order by priority DESC → id=2 first, id=1 second.
	if len(rows) != 2 || !idEquals(rows[0]["id"], 2) || !idEquals(rows[1]["id"], 1) {
		t.Fatalf("query rows order wrong: %v", rows)
	}

	// 6. datatable_count — by status open.
	cres := dispatch(t, h.datatableCount, map[string]string{
		"slug":  "events",
		"where": `{"status":"open"}`,
	})
	if cm := cres.(map[string]any); cm["count"] != 2 {
		t.Fatalf("count open = %v, want 2", cm["count"])
	}

	// 7. datatable_delete — by conditions (priority < 5).
	dres := dispatch(t, h.datatableDelete, map[string]string{
		"slug":       "events",
		"conditions": `[{"column":"priority","op":"lt","value":5}]`,
	})
	if dm := dres.(map[string]any); dm["deleted_count"] != 1 {
		t.Fatalf("delete count = %v, want 1", dm["deleted_count"])
	}

	// 8. datatable_list — now reflects 2 rows.
	listed2 := dispatch(t, h.datatableList, map[string]string{})
	rows2 := listed2.([]wfmcp.DataTableSummary)
	if len(rows2) != 1 || rows2[0].Slug != "events" || rows2[0].RowCount != 2 {
		t.Fatalf("datatable_list post-delete: %v", rows2)
	}

	// 9. datatable_drop — remove table.
	dispatch(t, h.datatableDrop, map[string]string{"slug": "events"})
	listed3 := dispatch(t, h.datatableList, map[string]string{})
	if rs := listed3.([]wfmcp.DataTableSummary); len(rs) != 0 {
		t.Fatalf("after drop: %v", rs)
	}
}

func TestMCP_DataTableErrors(t *testing.T) {
	h := newSelfTestHandlers()
	if err := dispatchErr(t, h.datatableGet, map[string]string{"slug": "nope"}); err == nil {
		t.Fatal("get of missing table should error")
	}
	if err := dispatchErr(t, h.datatableCreate, map[string]string{
		"slug":    "bad slug!",
		"columns": "id:string",
	}); err == nil {
		t.Fatal("invalid slug should error")
	}
	if err := dispatchErr(t, h.datatableCreate, map[string]string{
		"slug":    "good",
		"columns": "",
	}); err == nil {
		t.Fatal("missing columns should error")
	}
}
