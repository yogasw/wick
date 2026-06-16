package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// The templ audit-log page was removed in the SPA cutover; GET /manager/runs
// now 302s to the SPA, which reads the resolved JSON twin (apiAuditRuns in
// audit_api.go). The raw /api/runs + /api/runs/summary JSON endpoints below
// stay for dashboards and external monitoring.

// apiRuns returns paginated connector runs as JSON. Supports the same
// filter params as the audit log plus limit/offset for cursor-style access.
func (h *Handler) apiRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, _, _ := buildAuditFilter(r)

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	runs, err := h.connectors.ListRunsAudit(ctx, f, limit, offset)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	total, _ := h.connectors.CountRunsAudit(ctx, f)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"runs":   runs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// apiRunsSummary returns aggregate stats (total, success, error, avg latency)
// for the given filter window. Used by dashboards and external monitoring.
func (h *Handler) apiRunsSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, _, _ := buildAuditFilter(r)

	summary, err := h.connectors.SummariseRuns(ctx, f)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

// buildAuditFilter parses the standard audit query params into an
// AuditFilter plus the raw from/to strings (for pre-filling inputs).
func buildAuditFilter(r *http.Request) (connectors.AuditFilter, string, string) {
	q := r.URL.Query()
	f := connectors.AuditFilter{
		ConnectorID:  q.Get("connector_id"),
		OperationKey: q.Get("op"),
		Source:       q.Get("source"),
		Status:       q.Get("status"),
		UserID:       q.Get("user"),
	}
	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr != "" {
		if t, err := time.Parse("2006-01-02", fromStr); err == nil {
			f.From = &t
		} else {
			fromStr = ""
		}
	}
	if toStr != "" {
		if t, err := time.Parse("2006-01-02", toStr); err == nil {
			eod := t.Add(24*time.Hour - time.Second)
			f.To = &eod
		} else {
			toStr = ""
		}
	}
	return f, fromStr, toStr
}

// resolveRunConnectors bulk-loads the Connector rows referenced by
// run.ConnectorID. Missing or unknown IDs are silently omitted.
func (h *Handler) resolveRunConnectors(ctx context.Context, runs []entity.ConnectorRun) map[string]entity.Connector {
	out := map[string]entity.Connector{}
	seen := map[string]struct{}{}
	for _, run := range runs {
		seen[run.ConnectorID] = struct{}{}
	}
	for id := range seen {
		c, err := h.connectors.Get(ctx, id)
		if err != nil || c == nil {
			continue
		}
		out[id] = *c
	}
	return out
}
