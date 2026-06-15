package manager

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// jobDetailJSON is the shape served at GET /manager/api/jobs/{key}: the job
// identity + schedule settings plus the runtime config fields. Mirrors the
// data behind the legacy job_detail.templ, restricted to the presentation
// hints the SPA settings form + ConfigsForm need.
type jobDetailJSON struct {
	Key           string            `json:"key"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Icon          string            `json:"icon"`
	Schedule      string            `json:"schedule"`
	Enabled       bool              `json:"enabled"`
	MaxRuns       int               `json:"max_runs"`
	MaxTimeoutMin int               `json:"max_timeout_min"`
	TotalRuns     int               `json:"total_runs"`
	LastStatus    string            `json:"last_status"`
	CanConfigure  bool              `json:"can_configure"`
	Fields        []configFieldJSON `json:"fields"`
}

// toolDetailJSON is the shape served at GET /manager/api/tools/{key}: the
// tool identity plus its runtime config fields. Mirrors tool_detail.templ.
type toolDetailJSON struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Icon         string            `json:"icon"`
	CanConfigure bool              `json:"can_configure"`
	Fields       []configFieldJSON `json:"fields"`
}

// ownedConfigFields projects a config owner's rows into the SPA field
// schema, dropping hidden rows and blanking secret values (HasValue still
// signals a stored secret). Shared by the job + tool detail endpoints,
// mirroring apiConnectorDetail's projection so the field renderer behaves
// identically across all three surfaces.
func ownedConfigFields(rows []entity.Config) []configFieldJSON {
	fields := make([]configFieldJSON, 0, len(rows))
	for _, cfg := range rows {
		if cfg.Hidden {
			continue
		}
		f := configFieldJSON{
			Key:         cfg.Key,
			Type:        cfg.Type,
			Value:       cfg.Value,
			Options:     cfg.Options,
			Required:    cfg.Required,
			IsSecret:    cfg.IsSecret,
			HasValue:    cfg.Value != "",
			Description: descJSON(cfg.Description),
			VisibleWhen: cfg.VisibleWhen,
			ColOptions:  cfg.ColOptions,
			EnvOverride: cfg.EnvOverride,
		}
		if cfg.IsSecret {
			f.Value = ""
		}
		fields = append(fields, f)
	}
	return fields
}

// apiJobDetail serves GET /manager/api/jobs/{key}. Reuses GetJob + the
// configs ListOwned schema overlay, identical to jobDetailPage, returning
// JSON instead of rendering the templ page.
func (h *Handler) apiJobDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	j, err := h.svc.GetJob(ctx, key)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	rows := h.configs.ListOwned(j.Key)

	writeJSON(w, http.StatusOK, jobDetailJSON{
		Key:           j.Key,
		Name:          j.Name,
		Description:   j.Description,
		Icon:          j.Icon,
		Schedule:      j.Schedule,
		Enabled:       j.Enabled,
		MaxRuns:       j.MaxRuns,
		MaxTimeoutMin: jobMaxTimeoutMin(j),
		TotalRuns:     j.TotalRuns,
		LastStatus:    string(j.LastStatus),
		CanConfigure:  user != nil && user.IsAdmin(),
		Fields:        ownedConfigFields(rows),
	})
}

// apiUpdateJobSettings serves POST /manager/api/jobs/{key}/settings. Body:
// {"schedule":"…","enabled":true,"max_runs":0,"max_timeout_min":30}. Reuses
// UpdateSchedule (admin-gated by the route), returning JSON instead of the
// redirect the form handler does.
func (h *Handler) apiUpdateJobSettings(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var body struct {
		Schedule      string `json:"schedule"`
		Enabled       bool   `json:"enabled"`
		MaxRuns       int    `json:"max_runs"`
		MaxTimeoutMin int    `json:"max_timeout_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if body.MaxTimeoutMin <= 0 {
		body.MaxTimeoutMin = 30
	}
	if err := h.svc.UpdateSchedule(r.Context(), key, body.Schedule, body.Enabled, body.MaxRuns, body.MaxTimeoutMin); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// apiSetJobConfig serves POST /manager/api/jobs/{key}/configs/{configKey}.
// Body: {"value":"…"}. Reuses SetOwned (admin-gated by the route), mirroring
// setJobConfig but accepting a JSON body + returning JSON.
func (h *Handler) apiSetJobConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	configKey := r.PathValue("configKey")
	if _, err := h.svc.GetJob(r.Context(), key); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	h.setOwnedConfigJSON(w, r, key, configKey)
}

// apiRunJob serves POST /manager/api/jobs/{key}/run, the JSON twin of
// runJob. Returns the new run ID so the SPA can poll it, instead of relying
// on the Accept-header content negotiation the form route does.
func (h *Handler) apiRunJob(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	key := r.PathValue("key")
	runID, err := h.svc.RunManual(r.Context(), key, userID(user))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "run_id": runID})
}

// apiJobRun serves GET /manager/api/jobs/{key}/runs/{runID}, the JSON twin
// of getRun used by the SPA run poller. Status drives the poll loop;
// result carries the run output (markdown) on completion.
func (h *Handler) apiJobRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := h.svc.GetRun(r.Context(), runID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           run.ID,
		"job_id":       run.JobID,
		"status":       string(run.Status),
		"result":       run.Result,
		"triggered_by": string(run.TriggeredBy),
		"started_at":   run.StartedAt.Format(timeRFC3339),
		"ended_at":     run.EndedAt,
	})
}

// apiToolDetail serves GET /manager/api/tools/{key}. Reuses findTool + the
// configs ListOwned schema (with any registered decorator), identical to
// toolDetailPage.
func (h *Handler) apiToolDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")

	t, ok := h.findTool(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool not found"})
		return
	}
	rows := h.configs.ListOwned(t.Key)
	if dec, ok := h.configDecorators[t.Key]; ok {
		rows = dec(rows)
	}

	writeJSON(w, http.StatusOK, toolDetailJSON{
		Key:          t.Key,
		Name:         t.Name,
		Description:  t.Description,
		Icon:         t.Icon,
		CanConfigure: user != nil && user.IsAdmin(),
		Fields:       ownedConfigFields(rows),
	})
}

// apiSetToolConfig serves POST /manager/api/tools/{key}/configs/{configKey}.
// Body: {"value":"…"}. Reuses SetOwned (admin-gated by the route), mirroring
// setToolConfig.
func (h *Handler) apiSetToolConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	configKey := r.PathValue("configKey")
	if _, ok := h.findTool(key); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool not found"})
		return
	}
	h.setOwnedConfigJSON(w, r, key, configKey)
}

// setOwnedConfigJSON decodes a {"value":"…"} body and persists it through
// configs.SetOwned for the given owner/key, writing a JSON status. Shared
// by the job + tool config setters. SetOwned rejects unknown keys, so an
// invalid configKey surfaces as a 400.
func (h *Handler) setOwnedConfigJSON(w http.ResponseWriter, r *http.Request, owner, configKey string) {
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	if err := h.configs.SetOwned(r.Context(), owner, configKey, body.Value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// jobMaxTimeoutMin mirrors the templ maxTimeoutMin helper: defaults a
// zero/negative stored timeout to 30 for display.
func jobMaxTimeoutMin(j *entity.Job) int {
	if j.MaxTimeoutMin <= 0 {
		return 30
	}
	return j.MaxTimeoutMin
}
