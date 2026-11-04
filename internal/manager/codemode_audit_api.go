package manager

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/yogasw/wick/internal/connectors"
)

// AuditPlaces names where a run happened: the project of a session and a
// project's display name. Agent sessions and projects live on disk under
// the agents layout, which this package does not own, so the server wires
// it in (SetAuditPlaces). nil = runs show raw ids.
type AuditPlaces interface {
	SessionProject(sessionID string) string
	ProjectName(projectID string) string
}

// SetAuditPlaces wires the session/project resolver the audit pages use.
func (h *Handler) SetAuditPlaces(p AuditPlaces) {
	h.places = p
}

// codeRunJSON is one Code Mode script run in the audit.
type codeRunJSON struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	SessionID   string `json:"session_id"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	Via         string `json:"via"`
	Script      string `json:"script"`
	Lang        string `json:"lang"`
	Runner      string `json:"runner"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	Calls       int    `json:"calls"`
	CallErrors  int    `json:"call_errors"`
	DurationMs  int64  `json:"duration_ms"`
	QueuedMs    int64  `json:"queued_ms"`
	StartedAt   string `json:"started_at"`
}

// codeRunCountJSON is a top-N row with its key resolved to a label.
type codeRunCountJSON struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Runs   int64  `json:"runs"`
	Failed int64  `json:"failed"`
}

type codeRunSummaryJSON struct {
	Total         int64              `json:"total"`
	OK            int64              `json:"ok"`
	Failed        int64              `json:"failed"`
	ByStatus      map[string]int64   `json:"by_status"`
	AvgDurationMs float64            `json:"avg_duration_ms"`
	Calls         int64              `json:"calls"`
	CallErrors    int64              `json:"call_errors"`
	TopUsers      []codeRunCountJSON `json:"top_users"`
	TopProjects   []codeRunCountJSON `json:"top_projects"`
	TopScripts    []codeRunCountJSON `json:"top_scripts"`
	TopErrors     []codeRunCountJSON `json:"top_errors"`
}

type codeRunsJSON struct {
	Runs       []codeRunJSON      `json:"runs"`
	Page       int                `json:"page"`
	TotalPages int                `json:"total_pages"`
	Total      int                `json:"total"`
	PageSize   int                `json:"page_size"`
	Summary    codeRunSummaryJSON `json:"summary"`
}

// apiCodeRuns serves GET /manager/api/codemode-runs: every Code Mode
// script run (agent tool call, CLI, watch step, workflow node) with who
// ran it, where, how it ended, and a summary of the filtered window.
// Admin-only (gated by the route). Filters: user, project, session, via,
// status (ok|error|timeout|crashed|not_started, or failed = all but ok),
// script, from/to (YYYY-MM-DD), page.
func (h *Handler) apiCodeRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := connectors.CodeRunFilter{
		UserID:    q.Get("user"),
		ProjectID: q.Get("project"),
		SessionID: q.Get("session"),
		Via:       q.Get("via"),
		Status:    q.Get("status"),
		Script:    q.Get("script"),
	}
	f.From, f.To = auditDateRange(q.Get("from"), q.Get("to"))

	total, err := h.connectors.CountCodeRuns(ctx, f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	totalPages := int((total + int64(auditPageSize) - 1) / int64(auditPageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	page = min(max(page, 1), totalPages)
	runs, err := h.connectors.ListCodeRuns(ctx, f, auditPageSize, (page-1)*auditPageSize)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sum, err := h.connectors.SummariseCodeRuns(ctx, f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	users := map[string]string{}
	projects := map[string]string{}
	userName := func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := users[id]; ok {
			return n
		}
		users[id] = h.userLabel(ctx, id)
		return users[id]
	}
	projectName := func(id string) string {
		if id == "" {
			return ""
		}
		if n, ok := projects[id]; ok {
			return n
		}
		n := id
		if h.places != nil {
			if name := h.places.ProjectName(id); name != "" {
				n = name
			}
		}
		projects[id] = n
		return n
	}

	out := codeRunsJSON{
		Runs:       make([]codeRunJSON, 0, len(runs)),
		Page:       page,
		TotalPages: totalPages,
		Total:      int(total),
		PageSize:   auditPageSize,
		Summary: codeRunSummaryJSON{
			Total: sum.Total, OK: sum.OK, Failed: sum.Failed, ByStatus: sum.ByStatus,
			AvgDurationMs: sum.AvgDurationMs, Calls: sum.Calls, CallErrors: sum.CallErrors,
			TopUsers:    labelCounts(sum.TopUsers, userName),
			TopProjects: labelCounts(sum.TopProjects, projectName),
			TopScripts:  labelCounts(sum.TopScripts, nil),
			TopErrors:   labelCounts(sum.TopErrors, nil),
		},
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, codeRunJSON{
			ID: run.ID, UserID: run.UserID, UserName: userName(run.UserID),
			SessionID: run.SessionID, ProjectID: run.ProjectID, ProjectName: projectName(run.ProjectID),
			Via: string(run.Via), Script: run.Script, Lang: run.Lang, Runner: run.Runner,
			Status: string(run.Status), Error: run.ErrorMsg, Calls: run.Calls, CallErrors: run.CallErrors,
			DurationMs: run.DurationMs, QueuedMs: run.QueuedMs, StartedAt: run.StartedAt.Format(timeRFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func labelCounts(in []connectors.CodeRunCount, label func(string) string) []codeRunCountJSON {
	out := make([]codeRunCountJSON, 0, len(in))
	for _, c := range in {
		l := c.Key
		if label != nil {
			l = label(c.Key)
		}
		out = append(out, codeRunCountJSON{Key: c.Key, Label: l, Runs: c.Runs, Failed: c.Failed})
	}
	return out
}

// userLabel is a user's name, else email, else the raw id.
func (h *Handler) userLabel(ctx context.Context, id string) string {
	if h.users == nil {
		return id
	}
	u, err := h.users.GetUserByID(ctx, id)
	if err != nil || u == nil {
		return id
	}
	if u.Name != "" {
		return u.Name
	}
	if u.Email != "" {
		return u.Email
	}
	return id
}

// auditDateRange parses the from/to (YYYY-MM-DD) query values the audit
// pages share; to covers its whole day. A bad value is ignored.
func auditDateRange(fromStr, toStr string) (*time.Time, *time.Time) {
	var from, to *time.Time
	if t, err := time.Parse("2006-01-02", fromStr); err == nil {
		from = &t
	}
	if t, err := time.Parse("2006-01-02", toStr); err == nil {
		eod := t.Add(24*time.Hour - time.Second)
		to = &eod
	}
	return from, to
}
