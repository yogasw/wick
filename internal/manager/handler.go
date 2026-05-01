package manager

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager/view"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
)

//go:embed js
var StaticFS embed.FS

// Handler wires the /manager/* routes. Manager owns job scheduling,
// runtime configs for jobs and tools, and the admin surface for
// connector rows.
type Handler struct {
	svc        *Service
	configs    *configs.Service
	connectors *connectors.Service
	tags       *tags.Service
	tools      []tool.Tool
}

func NewHandler(svc *Service, configsSvc *configs.Service, connectorsSvc *connectors.Service, tagsSvc *tags.Service, tools []tool.Tool) *Handler {
	return &Handler{svc: svc, configs: configsSvc, connectors: connectorsSvc, tags: tagsSvc, tools: tools}
}

// Register wires /manager/* to mux. All pages require auth; admin-only
// actions (edits, regenerate, run) add RequireAdmin on top.
func (h *Handler) Register(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}
	authJob := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(authMidd.RequireJobAccess(next))
	}
	adminOnly := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(authMidd.RequireAdmin(next))
	}

	// Static assets
	mux.Handle("GET /modules/manager/", ui.StaticHandler("/modules/manager/", StaticFS))

	// Jobs — view/run gated by per-job access; admin mutations gate by RequireAdmin only
	// (admins must be able to manage disabled jobs, so settings routes skip RequireJobAccess).
	mux.Handle("GET /manager/jobs/{key}", authJob(h.jobDetailPage))
	mux.Handle("POST /manager/jobs/{key}/run", authJob(h.runJob))
	mux.Handle("POST /manager/jobs/{key}/settings", adminOnly(h.updateJobSettings))
	mux.Handle("POST /manager/jobs/{key}/configs/{configKey}", adminOnly(h.setJobConfig))
	mux.Handle("POST /manager/jobs/{key}/configs/{configKey}/regenerate", adminOnly(h.regenerateJobConfig))
	mux.Handle("GET /manager/jobs/{key}/runs/{runID}", authJob(h.getRun))

	// Tools
	mux.Handle("GET /manager/tools/{key}", auth(h.toolDetailPage))
	mux.Handle("POST /manager/tools/{key}/configs/{configKey}", adminOnly(h.setToolConfig))
	mux.Handle("POST /manager/tools/{key}/configs/{configKey}/regenerate", adminOnly(h.regenerateToolConfig))

	// Connectors — list + per-row detail with test panel and action menu.
	h.connectorRoutes(mux, authMidd)
}

// requiredMissingKeys returns the keys whose Required flag is set but
// value is empty.
func requiredMissingKeys(rows []entity.Config) []string {
	var out []string
	for _, v := range rows {
		if v.Required && v.Value == "" {
			out = append(out, v.Key)
		}
	}
	return out
}

// ── Jobs ──────────────────────────────────────────────────────

func (h *Handler) jobDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	j, err := h.svc.GetJob(ctx, key)
	if err != nil {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	rows := h.configs.ListOwned(j.Key)
	editConfig := r.URL.Query().Get("edit")
	view.JobDetailPage(j, rows, editConfig, user, "", h.jobBanner(j, rows)).Render(ctx, w)
}

func (h *Handler) jobBanner(j *entity.Job, rows []entity.Config) *ui.MissingEntry {
	missing := requiredMissingKeys(rows)
	if len(missing) == 0 {
		return nil
	}
	return &ui.MissingEntry{
		Scope:   "job",
		Key:     j.Key,
		Name:    j.Name,
		Icon:    j.Icon,
		URL:     "/manager/jobs/" + j.Key,
		Missing: missing,
	}
}

func (h *Handler) updateJobSettings(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	schedule := r.FormValue("schedule")
	enabled := boolParam(r, "enabled")
	maxRuns, _ := strconv.Atoi(r.FormValue("max_runs"))
	if err := h.svc.UpdateSchedule(r.Context(), key, schedule, enabled, maxRuns); err != nil {
		h.renderJobWithError(w, r, key, err.Error())
		return
	}
	http.Redirect(w, r, "/manager/jobs/"+key, http.StatusFound)
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	key := r.PathValue("key")
	if _, err := h.svc.RunManual(r.Context(), key, user.ID); err != nil {
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		h.renderJobWithError(w, r, key, err.Error())
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
		return
	}
	http.Redirect(w, r, "/manager/jobs/"+key, http.StatusFound)
}

func (h *Handler) setJobConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	configKey := r.PathValue("configKey")
	value := r.FormValue("value")
	if err := h.configs.SetOwned(r.Context(), key, configKey, value); err != nil {
		h.renderJobWithError(w, r, key, err.Error())
		return
	}
	http.Redirect(w, r, "/manager/jobs/"+key, http.StatusFound)
}

func (h *Handler) regenerateJobConfig(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "regenerate is only available on app-level variables", http.StatusBadRequest)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := h.svc.GetRun(r.Context(), runID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "run not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           run.ID,
		"job_id":       run.JobID,
		"status":       run.Status,
		"result":       run.Result,
		"triggered_by": run.TriggeredBy,
		"started_at":   run.StartedAt,
		"ended_at":     run.EndedAt,
	})
}

func (h *Handler) renderJobWithError(w http.ResponseWriter, r *http.Request, key string, msg string) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	j, err := h.svc.GetJob(ctx, key)
	if err != nil {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	rows := h.configs.ListOwned(j.Key)
	view.JobDetailPage(j, rows, "", user, msg, h.jobBanner(j, rows)).Render(ctx, w)
}

// ── Tools ─────────────────────────────────────────────────────

func (h *Handler) toolDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	t, ok := h.findTool(key)
	if !ok {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	rows := h.configs.ListOwned(t.Key)
	editKey := r.URL.Query().Get("edit")
	view.ToolDetailPage(t, rows, editKey, user, h.toolBanner(t, rows)).Render(ctx, w)
}

func (h *Handler) setToolConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	configKey := r.PathValue("configKey")
	if err := h.configs.SetOwned(r.Context(), key, configKey, r.FormValue("value")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/manager/tools/"+key, http.StatusFound)
}

func (h *Handler) regenerateToolConfig(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "regenerate is only available on app-level variables", http.StatusBadRequest)
}

func (h *Handler) findTool(key string) (tool.Tool, bool) {
	for _, t := range h.tools {
		if t.Key == key {
			return t, true
		}
	}
	return tool.Tool{}, false
}

func (h *Handler) toolBanner(t tool.Tool, rows []entity.Config) *ui.MissingEntry {
	missing := requiredMissingKeys(rows)
	if len(missing) == 0 {
		return nil
	}
	return &ui.MissingEntry{
		Scope:   "tool",
		Key:     t.Key,
		Name:    t.Name,
		Icon:    t.Icon,
		URL:     "/manager/tools/" + t.Key,
		Missing: missing,
	}
}

// ── helpers ───────────────────────────────────────────────────

func boolParam(r *http.Request, key string) bool {
	v := r.FormValue(key)
	return v == "on" || v == "true" || v == "1"
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept != "" && (accept == "application/json" || containsJSON(accept))
}

func containsJSON(s string) bool {
	for i := 0; i+len("application/json") <= len(s); i++ {
		if s[i:i+len("application/json")] == "application/json" {
			return true
		}
	}
	return false
}

// ensure context import used
var _ = context.Background
