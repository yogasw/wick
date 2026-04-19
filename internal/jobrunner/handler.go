// Package jobrunner mounts the operator-facing /jobs/{key} surface
// where users can run a job and see its recent history. Admin-only
// configuration (schedule, runtime configs) lives on /manager/jobs/{key}
// — this package intentionally does NOT expose those settings so
// operators can't accidentally change cadence or credentials.
//
// Both surfaces share the same manager.Service + configs.Service so
// run history and setup-required banners stay consistent across pages.
package jobrunner

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/jobrunner/view"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/pkg/ui"

	"github.com/rs/zerolog/log"
)

// Handler wires /jobs/{key} routes. svc is the job lifecycle service
// shared with the manager package; configsSvc is the runtime-config
// cache used for the setup-required banner.
type Handler struct {
	svc     *manager.Service
	configs *configs.Service
}

func NewHandler(svc *manager.Service, configsSvc *configs.Service) *Handler {
	return &Handler{svc: svc, configs: configsSvc}
}

// Register wires the operator routes onto mux. All pages require
// auth; the run endpoint is gated by the same auth layer because job
// visibility is enforced by the login middleware (default Private),
// not by RequireAdmin — any user with access to the job card can run
// it.
func (h *Handler) Register(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(authMidd.RequireJobAccess(next))
	}
	mux.Handle("GET /jobs/{key}", auth(h.jobPage))
	mux.Handle("POST /jobs/{key}/run", auth(h.runJob))
}

func (h *Handler) jobPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	j, err := h.svc.GetJob(ctx, key)
	if err != nil {
		ui.RenderNotFound(w, r, user, http.StatusNotFound)
		return
	}
	runs, _ := h.svc.ListRuns(ctx, key, 10)
	rows := h.configs.ListOwned(j.Key)
	view.JobPage(j, runs, user, "", jobBanner(j, rows)).Render(ctx, w)
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	key := r.PathValue("key")
	if _, err := h.svc.RunManual(ctx, key, user.ID); err != nil {
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		h.renderWithError(w, r, key, err.Error())
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
		return
	}
	http.Redirect(w, r, "/jobs/"+key, http.StatusFound)
}

func (h *Handler) renderWithError(w http.ResponseWriter, r *http.Request, key string, msg string) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	j, err := h.svc.GetJob(ctx, key)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("job", key).Msg("render job page with error")
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	runs, _ := h.svc.ListRuns(ctx, key, 10)
	rows := h.configs.ListOwned(j.Key)
	view.JobPage(j, runs, user, msg, jobBanner(j, rows)).Render(ctx, w)
}

// jobBanner builds a ScopedSetupBanner entry pointing at the admin
// settings page when the job still has Required configs empty. The
// banner is rendered below the navbar; admins get a Configure → link
// to /manager/jobs/{key}, non-admins just see the nudge.
func jobBanner(j *entity.Job, rows []entity.Config) *ui.MissingEntry {
	var missing []string
	for _, row := range rows {
		if row.Required && row.Value == "" {
			missing = append(missing, row.Key)
		}
	}
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

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	for i := 0; i+len("application/json") <= len(accept); i++ {
		if accept[i:i+len("application/json")] == "application/json" {
			return true
		}
	}
	return false
}
