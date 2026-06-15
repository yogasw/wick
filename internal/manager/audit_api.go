package manager

import (
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/connectors"
)

// auditRunJSON is one row in the cross-connector audit log the SPA renders.
// Mirrors the columns of the legacy audit_log.templ: the resolved connector
// identity (so the SPA can link to /connectors/{key}/{id}), operation,
// source, status, resolved user name, latency, and the run timestamp.
type auditRunJSON struct {
	ID            string `json:"id"`
	ConnectorID   string `json:"connector_id"`
	ConnectorKey  string `json:"connector_key"`
	ConnectorName string `json:"connector_name"`
	OperationKey  string `json:"operation_key"`
	Source        string `json:"source"`
	Status        string `json:"status"`
	UserID        string `json:"user_id"`
	UserName      string `json:"user_name"`
	LatencyMs     int    `json:"latency_ms"`
	StartedAt     string `json:"started_at"`
}

// auditJSON is the shape served at GET /manager/api/runs: the resolved +
// paginated audit rows plus the pagination envelope and a summary block, so
// the SPA can render the whole page from one call. Filters echo the request
// so the SPA can reconcile its state.
type auditJSON struct {
	Runs       []auditRunJSON        `json:"runs"`
	Source     string                `json:"source"`
	Status     string                `json:"status"`
	From       string                `json:"from"`
	To         string                `json:"to"`
	Page       int                   `json:"page"`
	TotalPages int                   `json:"total_pages"`
	Total      int                   `json:"total"`
	PageSize   int                   `json:"page_size"`
	Summary    connectors.RunSummary `json:"summary"`
}

// auditPageSize matches the legacy audit_log.templ page size so shared
// links land on equivalent pages across both surfaces.
const auditPageSize = 25

// apiAuditRuns serves GET /manager/api/runs: the cross-connector audit log
// as resolved JSON for the manager SPA. Admin-only (gated by the route).
// Reuses buildAuditFilter + ListRunsAudit/CountRunsAudit + the connector +
// user resolvers, identical to auditLogPage — no business-logic
// duplication. The raw /api/runs endpoint stays untouched for external
// integrations; this twin adds the name resolution + summary the UI needs.
func (h *Handler) apiAuditRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, fromStr, toStr := buildAuditFilter(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	total, _ := h.connectors.CountRunsAudit(ctx, f)
	totalPages := int((total + int64(auditPageSize) - 1) / int64(auditPageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	runs, err := h.connectors.ListRunsAudit(ctx, f, auditPageSize, (page-1)*auditPageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	connectorsByID := h.resolveRunConnectors(ctx, runs)
	usersByID := h.resolveRunUsers(ctx, runs)
	summary, _ := h.connectors.SummariseRuns(ctx, f)

	out := auditJSON{
		Runs:       make([]auditRunJSON, 0, len(runs)),
		Source:     f.Source,
		Status:     f.Status,
		From:       fromStr,
		To:         toStr,
		Page:       page,
		TotalPages: totalPages,
		Total:      int(total),
		PageSize:   auditPageSize,
		Summary:    summary,
	}
	for _, run := range runs {
		row := auditRunJSON{
			ID:           run.ID,
			ConnectorID:  run.ConnectorID,
			OperationKey: run.OperationKey,
			Source:       string(run.Source),
			Status:       string(run.Status),
			UserID:       run.UserID,
			UserName:     usersByID[run.UserID],
			LatencyMs:    run.LatencyMs,
			StartedAt:    run.StartedAt.Format(timeRFC3339),
		}
		if c, ok := connectorsByID[run.ConnectorID]; ok {
			row.ConnectorKey = c.Key
			row.ConnectorName = c.Label
		}
		out.Runs = append(out.Runs, row)
	}
	writeJSON(w, http.StatusOK, out)
}
