package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestAPIAuditRuns(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create row: %v", err)
	}
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceMCP)

	req := adminReq(t, http.MethodGet, "/manager/api/runs", nil)
	rec := httptest.NewRecorder()
	h.apiAuditRuns(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got auditJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Total != 2 || len(got.Runs) != 2 {
		t.Fatalf("total/runs = %d/%d, want 2/2", got.Total, len(got.Runs))
	}
	if got.Page != 1 || got.TotalPages != 1 || got.PageSize != auditPageSize {
		t.Errorf("pagination wrong: page=%d pages=%d size=%d", got.Page, got.TotalPages, got.PageSize)
	}
	if got.Summary.Total != 2 || got.Summary.Succeeded != 2 {
		t.Errorf("summary wrong: %+v", got.Summary)
	}
	r0 := got.Runs[0]
	if r0.ConnectorKey != "slack" || r0.ConnectorName != "Prod" {
		t.Errorf("connector identity not resolved: %+v", r0)
	}
	if r0.StartedAt == "" {
		t.Errorf("run row should carry started_at")
	}
}

func TestAPIAuditRunsFilterBySource(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	seedRun(t, svc, row.ID, entity.ConnectorRunSourceMCP)

	cases := []struct {
		name      string
		query     string
		wantTotal int
		wantSrc   string
	}{
		{name: "mcp only", query: "?source=mcp", wantTotal: 1, wantSrc: string(entity.ConnectorRunSourceMCP)},
		{name: "test only", query: "?source=test", wantTotal: 1, wantSrc: string(entity.ConnectorRunSourceTest)},
		{name: "no filter", query: "", wantTotal: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := adminReq(t, http.MethodGet, "/manager/api/runs"+tc.query, nil)
			rec := httptest.NewRecorder()
			h.apiAuditRuns(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var got auditJSON
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Total != tc.wantTotal {
				t.Fatalf("total = %d, want %d", got.Total, tc.wantTotal)
			}
			if tc.wantSrc != "" {
				for _, r := range got.Runs {
					if r.Source != tc.wantSrc {
						t.Errorf("source filter leaked: %+v", r)
					}
				}
			}
		})
	}
}

func TestAPIAuditRunsPagination(t *testing.T) {
	h, svc := newHistoryHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	for i := 0; i < auditPageSize+3; i++ {
		seedRun(t, svc, row.ID, entity.ConnectorRunSourceTest)
	}

	req := adminReq(t, http.MethodGet, "/manager/api/runs?page=2", nil)
	rec := httptest.NewRecorder()
	h.apiAuditRuns(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got auditJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Page != 2 || got.TotalPages != 2 {
		t.Errorf("pagination wrong: page=%d pages=%d", got.Page, got.TotalPages)
	}
	if len(got.Runs) != 3 {
		t.Errorf("page 2 runs = %d, want 3 (remainder)", len(got.Runs))
	}
}
