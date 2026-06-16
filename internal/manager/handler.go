package manager

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
)

// Handler wires the /manager/* routes. Manager owns job scheduling,
// runtime configs for jobs and tools, and the admin surface for
// connector rows.
type Handler struct {
	svc        *Service
	configs    *configs.Service
	connectors *connectors.Service
	tags       *tags.Service
	users      *login.Service
	tools      []tool.Tool
	// custom is the custom-connector service (nil until SetCustomConnectors
	// runs at boot). Owns the /manager/connectors/custom/* builder flows.
	custom *customconn.Service
	// configDecorators: per-tool key → function that can mutate config rows
	// before they are rendered (e.g. to inject dynamic dropdown options).
	configDecorators map[string]func([]entity.Config) []entity.Config

	// oauthSecret is a per-process random secret used to HMAC-sign OAuth state tokens.
	oauthSecret []byte
	// oauthPending stores in-flight OAuth state tokens keyed by the state string.
	// Values are oauthStateEntry (defined in oauth.go).
	oauthPending sync.Map
}

// RegisterConfigDecorator registers a function that is called on the config
// rows for toolKey just before the manager detail page is rendered. Use it
// to inject dynamic Options (e.g. workspace names) into specific rows.
func (h *Handler) RegisterConfigDecorator(toolKey string, fn func([]entity.Config) []entity.Config) {
	h.configDecorators[toolKey] = fn
}

func NewHandler(svc *Service, configsSvc *configs.Service, connectorsSvc *connectors.Service, tagsSvc *tags.Service, usersSvc *login.Service, tools []tool.Tool) *Handler {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		log.Warn().Err(err).Msg("manager oauth: failed to generate secret; using fallback")
		secret = []byte("wick-manager-oauth-fallback-secret")
	}
	return &Handler{
		svc:              svc,
		configs:          configsSvc,
		connectors:       connectorsSvc,
		tags:             tagsSvc,
		users:            usersSvc,
		tools:            tools,
		configDecorators: make(map[string]func([]entity.Config) []entity.Config),
		oauthSecret:      secret,
	}
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

	// Manager SPA — the Svelte module owns every manager screen, served
	// inside the host ui.Layout chrome at the original /manager/* paths
	// (serveSPAShell). The Vite bundle assets are served separately from
	// the all:dist embed at spaAssetBase (/manager/_app/, spa_handler.go).
	h.registerSPAAssets(mux, authMidd)

	// SPA shell page routes. Each manager *page* GET renders the thin-shell
	// inside the host chrome; the SPA's client router resolves the rest of
	// the path. The bare /manager lands on the connectors index (client
	// route "/"). The {path...} catch-all covers every other client-side
	// route on deep-link / refresh (audit, custom builder, connector rows)
	// while staying less specific than the /manager/api/* + POST routes, so
	// those always win. Jobs / runs keep their own routes below because they
	// need a stricter auth gate (per-job access / admin) than base auth.
	mux.Handle("GET /manager", auth(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("GET /manager/{path...}", auth(http.HandlerFunc(h.serveSPAShell)))

	// Jobs — view/run gated by per-job access; admin mutations gate by RequireAdmin only
	// (admins must be able to manage disabled jobs, so settings routes skip RequireJobAccess).
	mux.Handle("GET /manager/jobs/{key}", authJob(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/jobs/{key}/run", authJob(h.runJob))
	mux.Handle("POST /manager/jobs/{key}/cancel", authJob(h.cancelJob))
	mux.Handle("POST /manager/jobs/{key}/settings", adminOnly(h.updateJobSettings))
	mux.Handle("POST /manager/jobs/{key}/configs/{configKey}", adminOnly(h.setJobConfig))
	mux.Handle("POST /manager/jobs/{key}/configs/{configKey}/regenerate", adminOnly(h.regenerateJobConfig))
	mux.Handle("GET /manager/jobs/{key}/runs/{runID}", authJob(h.getRun))

	// Jobs — JSON read/mutate twins for the manager SPA (coexist with the
	// templ form routes above). Run/poll mirror the form routes' JSON
	// behaviour on the /manager/api surface so the SPA stays consistent.
	mux.Handle("GET /manager/api/jobs/{key}", authJob(h.apiJobDetail))
	mux.Handle("POST /manager/api/jobs/{key}/run", authJob(h.apiRunJob))
	mux.Handle("GET /manager/api/jobs/{key}/runs/{runID}", authJob(h.apiJobRun))
	mux.Handle("POST /manager/api/jobs/{key}/settings", adminOnly(h.apiUpdateJobSettings))
	mux.Handle("POST /manager/api/jobs/{key}/configs/{configKey}", adminOnly(h.apiSetJobConfig))

	// Tools
	mux.Handle("GET /manager/tools/{key}", auth(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("POST /manager/tools/{key}/configs/{configKey}", adminOnly(h.setToolConfig))
	mux.Handle("POST /manager/tools/{key}/configs/{configKey}/regenerate", adminOnly(h.regenerateToolConfig))

	// Tools — JSON read/mutate twins for the manager SPA.
	mux.Handle("GET /manager/api/tools/{key}", auth(h.apiToolDetail))
	mux.Handle("POST /manager/api/tools/{key}/configs/{configKey}", adminOnly(h.apiSetToolConfig))

	// Connectors — list + per-row detail with test panel and action menu.
	h.connectorRoutes(mux, authMidd)
	// Custom connectors — admin-only builder flows (paste/MCP/manual).
	h.customConnectorRoutes(mux, authMidd)
	// SSO verification key for custom-connector MCP servers — public.
	mux.HandleFunc("GET /.well-known/wick-pubkey.pem", h.customPubkeyPEM)
	// OAuth — public routes for connector user OAuth flows (no auth middleware).
	h.oauthRoutes(mux)

	// Audit log — cross-connector run history (admin only). The page route
	// renders the SPA shell (client route "/audit"); the JSON twin stays.
	mux.Handle("GET /manager/runs", adminOnly(http.HandlerFunc(h.serveSPAShell)))
	mux.Handle("GET /api/runs", adminOnly(h.apiRuns))
	mux.Handle("GET /api/runs/summary", adminOnly(h.apiRunsSummary))
	// Audit log — resolved JSON twin for the manager SPA (connector + user
	// names + summary in one call; coexists with the raw /api/runs above).
	mux.Handle("GET /manager/api/runs", adminOnly(h.apiAuditRuns))
}

// ── Jobs ──────────────────────────────────────────────────────
//
// The templ job/tool detail pages were removed in the SPA cutover; their
// GET routes now 302 to the SPA, which reads the JSON twins (apiJobDetail /
// apiToolDetail). The legacy form-POST mutation handlers below stay live;
// on error they fall back to a plain text response (renderJobWithError)
// instead of re-rendering the deleted page.

func (h *Handler) updateJobSettings(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	schedule := r.FormValue("schedule")
	enabled := boolParam(r, "enabled")
	maxRuns, _ := strconv.Atoi(r.FormValue("max_runs"))
	maxTimeoutMin, _ := strconv.Atoi(r.FormValue("max_timeout_min"))
	if maxTimeoutMin <= 0 {
		maxTimeoutMin = 30
	}
	if err := h.svc.UpdateSchedule(r.Context(), key, schedule, enabled, maxRuns, maxTimeoutMin); err != nil {
		h.renderJobWithError(w, r, key, err.Error())
		return
	}
	http.Redirect(w, r, "/manager/jobs/"+key, http.StatusFound)
}

func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := h.svc.CancelJob(r.Context(), key); err != nil {
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
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
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

// renderJobWithError reports a failed legacy job form-POST. The templ job
// page it used to re-render was removed in the SPA cutover, so it now
// returns the message as plain text (400). The SPA path surfaces errors
// through the JSON twins instead.
func (h *Handler) renderJobWithError(w http.ResponseWriter, _ *http.Request, _ string, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}

// ── Tools ─────────────────────────────────────────────────────

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
