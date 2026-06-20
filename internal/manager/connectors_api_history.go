package manager

import (
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// historyRunJSON is one run row in the per-connector audit log the SPA
// ConnectorHistory page renders. It mirrors the columns + expand panel of
// the legacy connector_history.templ: summary fields plus the full
// request/response JSON for the expandable detail.
type historyRunJSON struct {
	ID           string `json:"id"`
	OperationKey string `json:"operation_key"`
	Source       string `json:"source"`
	Status       string `json:"status"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	ErrorMsg     string `json:"error_msg"`
	LatencyMs    int    `json:"latency_ms"`
	HTTPStatus   int    `json:"http_status"`
	IPAddress    string `json:"ip_address"`
	UserAgent    string `json:"user_agent"`
	RequestJSON  string `json:"request_json"`
	ResponseJSON string `json:"response_json"`
	StartedAt    string `json:"started_at"`
}

// historyOpJSON is one entry in the op filter dropdown.
type historyOpJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// historyUserJSON is one entry in the user filter dropdown: the distinct
// users who appear in the current run set, id→display.
type historyUserJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// historyJSON is the shape served at
// GET /manager/api/connectors/{key}/{id}/history. It carries the filtered
// + paginated runs plus the dropdown option sets (ops, users) and the
// pagination envelope, so the SPA can render the whole page from one call.
type historyJSON struct {
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Runs       []historyRunJSON  `json:"runs"`
	Ops        []historyOpJSON   `json:"ops"`
	Users      []historyUserJSON `json:"users"`
	Page       int               `json:"page"`
	TotalPages int               `json:"total_pages"`
	Total      int               `json:"total"`
	PageSize   int               `json:"page_size"`
}

// historyPageSize matches the legacy connector_history.templ page size so
// shared links land on equivalent pages across both surfaces.
const historyPageSize = 10

// apiConnectorHistory serves the per-row run audit log as JSON for the
// manager SPA. Filters (op/source/status/user) + page come from query
// params, mirroring connectorHistoryPage exactly: same RunFilter, same
// pageSize, same Count/List service calls + user resolution. No
// business-logic duplication — it reuses ListRunsFiltered/CountRunsFiltered.
func (h *Handler) apiConnectorHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	mod, ok := h.connectors.Module(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector"})
		return
	}
	row, errResp, ok := h.loadVisibleRow(r, user)
	if !ok {
		writeJSON(w, errResp.status, map[string]string{"error": errResp.msg})
		return
	}

	filter := connectors.RunFilter{
		OperationKey: r.URL.Query().Get("op"),
		Source:       r.URL.Query().Get("source"),
		Status:       r.URL.Query().Get("status"),
		UserID:       r.URL.Query().Get("user"),
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	total, _ := h.connectors.CountRunsFiltered(ctx, row.ID, filter)
	totalPages := int((total + int64(historyPageSize) - 1) / int64(historyPageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	runs, _ := h.connectors.ListRunsFiltered(ctx, row.ID, filter, historyPageSize, (page-1)*historyPageSize)
	usersByID := h.resolveRunUsers(ctx, runs)

	out := historyJSON{
		Key:        mod.Meta.Key,
		Name:       mod.Meta.Name,
		ID:         row.ID,
		Label:      row.Label,
		Runs:       make([]historyRunJSON, 0, len(runs)),
		Ops:        make([]historyOpJSON, 0, len(mod.AllOps())),
		Users:      make([]historyUserJSON, 0, len(usersByID)),
		Page:       page,
		TotalPages: totalPages,
		Total:      int(total),
		PageSize:   historyPageSize,
	}
	for _, op := range mod.AllOps() {
		out.Ops = append(out.Ops, historyOpJSON{Key: op.Key, Name: op.Name})
	}
	for id, name := range usersByID {
		out.Users = append(out.Users, historyUserJSON{ID: id, Name: name})
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, historyRunJSON{
			ID:           run.ID,
			OperationKey: run.OperationKey,
			Source:       string(run.Source),
			Status:       string(run.Status),
			UserID:       run.UserID,
			UserName:     usersByID[run.UserID],
			ErrorMsg:     run.ErrorMsg,
			LatencyMs:    run.LatencyMs,
			HTTPStatus:   run.HTTPStatus,
			IPAddress:    run.IPAddress,
			UserAgent:    run.UserAgent,
			RequestJSON:  run.RequestJSON,
			ResponseJSON: run.ResponseJSON,
			StartedAt:    run.StartedAt.Format(timeRFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// timeRFC3339 is the layout the history JSON emits StartedAt in so the
// SPA can parse + format it client-side (relative + absolute on hover).
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// _ keeps the entity import alive for the run projection above.
var _ entity.ConnectorRun
