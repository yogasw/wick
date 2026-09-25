package agentmemory

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// store persists the Agent Memory settings and answers access questions.
// Wired once by RegisterRoutes; nil until then, and every gate fails closed.
var store ConfigStore

// Budgets for the backend calls behind each endpoint. They differ by an order
// of magnitude because the work does: a status read is one SQLite query, while
// doctor walks the local harness stores and a backfill imports sessions.
const (
	statusTimeout   = 20 * time.Second
	healthTimeout   = 90 * time.Second
	backfillTimeout = 10 * time.Minute
	// compactTimeout bounds a VACUUM + FTS rebuild of the whole database.
	compactTimeout = 10 * time.Minute
	// startTimeout bounds waiting for a daemon to answer its health path.
	startTimeout = 45 * time.Second
	// projectsTimeout covers the list AND one briefing per project. The
	// briefings run concurrently, so this grows with the slowest call and
	// the concurrency limit, not with the project count.
	projectsTimeout = 45 * time.Second
)

// defaultHandoffLimit matches the backend's own listing default.
const defaultHandoffLimit = 50

// RegisterRoutes wires the daemon-control and panel-data endpoints for every
// registered backend under /agentmemory/<id>/… (relative to the hosting tool's
// base) and backs the core with the config store.
//
// Unlike airouter there is no proxy mount here: the panel is wick's own FE
// reading these endpoints, so nothing of the backend's is exposed at the wick
// root (PLAN §13.2).
func RegisterRoutes(r tool.Router, cfg ConfigStore) {
	store = cfg
	ApplySettings()

	// Registry-wide list so the FE can enumerate backends before it knows
	// which one is selected.
	r.GET("/agentmemory/backends", listBackends)

	for _, be := range List() {
		be := be
		p := "/agentmemory/" + be.Desc.ID

		// Daemon control.
		r.GET(p+"/status", ctl(be, statusHandler))
		r.GET(p+"/logs", ctl(be, logsHandler))
		r.POST(p+"/start", ctl(be, startHandler))
		r.POST(p+"/stop", ctl(be, stopHandler))
		r.POST(p+"/restart", ctl(be, restartHandler))
		r.POST(p+"/install", ctl(be, installHandler))
		r.POST(p+"/test", ctl(be, testHandler))

		// Daemon settings.
		r.GET(p+"/settings", ctl(be, getSettings))
		r.POST(p+"/settings", ctl(be, saveSettings))

		// Panel data.
		r.GET(p+"/projects", ctl(be, projectsHandler))
		r.GET(p+"/health", ctl(be, healthHandler))
		r.GET(p+"/handoffs", ctl(be, handoffsHandler))
		r.GET(p+"/search", ctl(be, searchHandler))
		r.POST(p+"/backfill/preview", ctl(be, backfillPreview))
		r.POST(p+"/backfill/run", ctl(be, backfillRun))
		r.POST(p+"/compact", ctl(be, compactHandler))

		// Wiki, Handoffs and the retention sweep (handlers_wiki.go).
		registerWikiRoutes(r, be, p)
	}
}

// ctl wraps a per-backend handler with the shared gate: master switch on +
// caller has access.
func ctl(be *Backend, fn func(*Backend, *tool.Ctx)) tool.HandlerFunc {
	return func(c *tool.Ctx) {
		if !allowed(c) {
			return
		}
		fn(be, c)
	}
}

// allowed gates every endpoint. A disabled master answers 404 so the feature
// looks absent rather than forbidden; a non-admin gets 403.
func allowed(c *tool.Ctx) bool {
	if store == nil || !store.Enabled() {
		c.Error(http.StatusNotFound, "agent memory disabled")
		return false
	}
	if !store.AccessAllowed(c.Context()) {
		c.Error(http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// backendInfo is the switcher payload for one backend.
type backendInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Blurb       string `json:"blurb"`
	Icon        string `json:"icon"`
	GitHubURL   string `json:"github_url,omitempty"`
	InstallKind string `json:"install_kind,omitempty"`
	HasData     bool   `json:"has_data"`
}

func listBackends(c *tool.Ctx) {
	if !allowed(c) {
		return
	}
	out := make([]backendInfo, 0, len(List()))
	for _, be := range List() {
		kind := be.Desc.InstallKind
		if kind == "" {
			kind = InstallManual
		}
		out = append(out, backendInfo{
			ID:          be.Desc.ID,
			Name:        be.Desc.DisplayName,
			Blurb:       be.Desc.Blurb,
			Icon:        be.Desc.IconSVG,
			GitHubURL:   be.Desc.GitHubURL,
			InstallKind: string(kind),
			HasData:     be.Desc.Data != nil,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"backends": out})
}

// ── daemon control + overview ────────────────────────────────────────

// Overview is the Overview tab's payload in one request: how the daemon is
// doing, what the store holds, what it costs, how it is configured, and who
// depends on it.
//
// The store block is separate from the daemon block on purpose. They answer
// different questions and can disagree — a daemon can be up with an empty
// store, and a store can be readable while the daemon wick manages is down —
// so a failure to read one never blanks the other (PLAN §13.5).
type Overview struct {
	Backend   backendInfo   `json:"backend"`
	Daemon    Status        `json:"daemon"`
	Settings  Settings      `json:"settings"`
	Resources Resources     `json:"resources"`
	UsedBy    []InstanceRef `json:"used_by"`
	// AutostartLock says whether the autostart control is forced on, and
	// why — the same instances as UsedBy, with the sentence to show.
	AutostartLock AutostartLock `json:"autostart_lock"`
	Store         *StoreStatus  `json:"store,omitempty"`
	// StoreError explains an unreadable store, and StoreReason names the
	// cause in one machine-checkable token when wick knows it.
	StoreError  string `json:"store_error,omitempty"`
	StoreReason string `json:"store_reason,omitempty"`
}

func statusHandler(be *Backend, c *tool.Ctx) {
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	st := be.Mgr.Status(ctx)
	set, lock := settingsForUI(be.Desc.ID)
	ov := Overview{
		Backend:       backendInfo{ID: be.Desc.ID, Name: be.Desc.DisplayName, Blurb: be.Desc.Blurb, GitHubURL: be.Desc.GitHubURL, HasData: be.Desc.Data != nil},
		Daemon:        st,
		Settings:      set,
		Resources:     probeResources(be.Mgr.PID(), effectiveDataDir(be, set)),
		UsedBy:        lock.UsedBy,
		AutostartLock: lock,
	}
	if be.Desc.Data != nil {
		if s, err := be.Desc.Data.StoreStatus(ctx, connFor(be, set)); err != nil {
			ov.StoreError, ov.StoreReason = err.Error(), reasonFor(err, st)
		} else {
			ov.Store = s
		}
	}
	c.JSON(http.StatusOK, ov)
}

func logsHandler(be *Backend, c *tool.Ctx) {
	c.JSON(http.StatusOK, map[string]string{"logs": be.Mgr.Logs()})
}

func startHandler(be *Backend, c *tool.Ctx) {
	// Re-apply settings first: a port or store change saved while the daemon
	// was down only takes effect on a start, and starting with the previous
	// options would quietly contradict the settings page.
	ApplySettings()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), startTimeout)
	defer cancel()
	if err := be.Mgr.StartAndWait(ctx); err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, be.Mgr.Status(ctx))
}

func stopHandler(be *Backend, c *tool.Ctx) {
	be.Mgr.StopProcess()
	c.JSON(http.StatusOK, be.Mgr.Status(c.Context()))
}

func restartHandler(be *Backend, c *tool.Ctx) {
	ApplySettings()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), startTimeout)
	defer cancel()
	if err := be.Mgr.Restart(ctx); err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, be.Mgr.Status(ctx))
}

func installHandler(be *Backend, c *tool.Ctx) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), startTimeout)
	defer cancel()
	out, err := be.Mgr.Install(ctx)
	if err != nil {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"output": out})
}

// testHandler is the "Test connection" button: probe the daemon's health path
// and, when it answers, read the store so the result carries something
// verifiable (version + counts) rather than a bare green dot. It exists to
// stop a provider toggle being switched on towards a server that is not
// there (PLAN §18.3).
func testHandler(be *Backend, c *tool.Ctx) {
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	set := settingsFor(be.Desc.ID)
	res := map[string]any{
		"base_url":    be.Mgr.BaseURL(),
		"health_path": be.Desc.HealthPath,
		"ok":          be.Mgr.probeHealth(),
	}
	if !res["ok"].(bool) {
		res["error"] = "no 200 from " + be.Mgr.BaseURL() + be.Desc.HealthPath
		c.JSON(http.StatusOK, res)
		return
	}
	if be.Desc.Data != nil {
		if s, err := be.Desc.Data.StoreStatus(ctx, connFor(be, set)); err == nil {
			res["version"] = s.Version
			res["counts"] = s.Counts
			res["data_dir"] = s.DataDir
		} else {
			res["store_error"] = err.Error()
		}
	}
	c.JSON(http.StatusOK, res)
}

// ── settings ─────────────────────────────────────────────────────────

func getSettings(be *Backend, c *tool.Ctx) {
	set, lock := settingsForUI(be.Desc.ID)
	c.JSON(http.StatusOK, map[string]any{
		"settings": set,
		"used_by":  lock.UsedBy,
		// Why the autostart control is disabled, in the words the FE shows.
		"autostart_lock": lock,
		"default_port":   be.Desc.PrefPort,
	})
}

// saveSettings persists the daemon settings. Autostart is stored as submitted
// but reported back with the lock applied — an instance that uses the backend
// keeps it effectively on no matter what was posted (PLAN §10.3).
func saveSettings(be *Backend, c *tool.Ctx) {
	cur := settingsFor(be.Desc.ID)
	next := Settings{
		DataDir:             strings.TrimSpace(c.Form("data_dir")),
		Port:                formInt(c, "port", cur.Port),
		EnableWeb:           formBool(c, "enable_web", cur.EnableWeb),
		Autostart:           formBool(c, "autostart", cur.Autostart),
		BackfillMaxSessions: formInt(c, "backfill_max_sessions", cur.BackfillMaxSessions),
		Tuning:              tuningFromForm(c, cur.Tuning),
	}
	if next.Port < 0 || next.Port > 65535 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "port must be between 0 and 65535 (0 = the backend default)"})
		return
	}
	if next.DataDir != "" && !filepathIsAbs(next.DataDir) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "data_dir must be an absolute path"})
		return
	}
	if err := store.SaveSettings(c.Context(), be.Desc.ID, next); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ApplySettings()
	// Settings land on the next start, so say so rather than letting the page
	// imply a running daemon already moved to the new port or store.
	set, lock := settingsForUI(be.Desc.ID)
	c.JSON(http.StatusOK, map[string]any{
		"settings":        set,
		"autostart_lock":  lock,
		"restart_pending": be.Mgr.spawnedHere(),
	})
}

// ── panel data ───────────────────────────────────────────────────────

func projectsHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), projectsTimeout)
	defer cancel()

	conn := connFor(be, set)
	rows, err := src.Projects(ctx, conn)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	if rows == nil {
		rows = []ProjectRow{}
	}
	if br, ok := src.(ProjectBriefer); ok {
		attachBriefings(ctx, br, conn, rows)
	}
	c.JSON(http.StatusOK, map[string]any{"projects": rows})
}

// briefingConcurrency bounds the fan-out. One briefing is a handful of local
// SQLite counts, so the limit is about not hammering a single-process daemon
// on a store with many projects, not about wick's own cost.
const briefingConcurrency = 4

// attachBriefings fills each row's per-project counters, one scoped call per
// project, in place.
//
// Failures are recorded on the row they belong to and nothing else: a project
// the backend refuses to brief (renamed, removed under us) must not cost the
// other rows their numbers, and must not cost the page its project list —
// which is why this never returns an error. A row that failed keeps Briefing
// nil, so the FE shows an absence with a reason rather than a zero.
func attachBriefings(ctx context.Context, br ProjectBriefer, conn Conn, rows []ProjectRow) {
	sem := make(chan struct{}, briefingConcurrency)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(r *ProjectRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := br.ProjectBriefing(ctx, conn, ReadScope{Workspace: r.Workspace, Project: r.Project})
			if err != nil {
				r.BriefingError = err.Error()
				return
			}
			r.Briefing = b
		}(&rows[i])
	}
	wg.Wait()
}

func healthHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), healthTimeout)
	defer cancel()

	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rep, err := src.Health(ctx, connFor(be, set), scope)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	// wick's own check, added after the backend's. It is local and cheap, and
	// it answers a question no backend can: two wick projects whose folders
	// land in one ai-memory project (PLAN §18.1).
	rep.Collisions = RunCollisionCheck()
	c.JSON(http.StatusOK, rep)
}

func handoffsHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit := formInt(c, "limit", defaultHandoffLimit)
	rows, err := src.Handoffs(ctx, connFor(be, set), scope, limit)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	if rows == nil {
		rows = []Handoff{}
	}
	c.JSON(http.StatusOK, map[string]any{"handoffs": rows})
}

// searchNote is handed to the FE with every search result. The backend's query
// parser matches whole FTS tokens, so `kasir` finds nothing while
// `kasir_prod_db` finds the page (verified 2026-09-25). Without this the user
// concludes their memory is empty when it is only their query that missed.
const searchNote = "Whole-token match: an identifier only matches in full — searching \"kasir\" finds nothing that \"kasir_prod_db\" finds."

func searchHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusOK, map[string]any{"hits": []SearchHit{}, "note": searchNote})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), statusTimeout)
	defer cancel()

	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	hits, err := src.Search(ctx, connFor(be, set), scope, q)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	if hits == nil {
		hits = []SearchHit{}
	}
	c.JSON(http.StatusOK, map[string]any{"hits": hits, "query": q, "note": searchNote})
}

// ── backfill ─────────────────────────────────────────────────────────

// backfillWarning is attached to every preview so the confirmation the FE
// shows carries the consequence, not just a question. A forced run re-imports
// sessions that are already in the store and observations do not dedupe, so
// the same facts are counted again and recall ranking drifts with them
// (PLAN §11.1).
const backfillWarning = "A forced import re-imports sessions the store already has. Sessions and latest pages dedupe, observations do NOT — every forced run adds them again. Import one missing session at a time instead."

func backfillPreview(be *Backend, c *tool.Ctx) { runBackfill(be, c, true) }
func backfillRun(be *Backend, c *tool.Ctx)     { runBackfill(be, c, false) }

// runBackfill executes one import. The gate on a real forced run is deliberate
// and lives here rather than in the FE: force is the one flag in this feature
// that silently multiplies stored data, so the backend refuses it unless the
// caller says confirm=true in the same request (PLAN §11.2).
func runBackfill(be *Backend, c *tool.Ctx, dryRun bool) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	scope, err := scopeFromForm(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	force := formBool(c, "force", false)
	if !dryRun && force && !formBool(c, "confirm", false) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "a forced import needs confirm=true",
			"warning": backfillWarning,
		})
		return
	}
	req := BackfillRequest{
		Scope:       scope,
		DryRun:      dryRun,
		Force:       force,
		Session:     strings.TrimSpace(c.Form("session")),
		MaxSessions: formInt(c, "max_sessions", set.MaxSessions()),
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), backfillTimeout)
	defer cancel()

	rep, err := src.Backfill(ctx, connFor(be, set), req)
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"report": rep, "request": req, "warning": backfillWarning})
}

// ── compact ──────────────────────────────────────────────────────────

// compactWarning is the consequence the confirmation has to state. Compaction
// is not destructive — it deletes nothing — so the danger it carries is the
// one an operator would otherwise discover by watching every agent stall: an
// exclusive lock for the length of a full database rewrite (verified from
// `ai-memory compact --help`, 2.4.0).
const compactWarning = "Compaction deletes nothing, but it takes an exclusive lock and rewrites the whole database: every write blocks until it finishes, and it needs free disk space of roughly the database's own size. Check \"reclaimable\" first — when it is 0 there is nothing to win."

// compactHandler reclaims free database pages. Like a forced backfill it
// refuses to run without confirm=true in the same request, so the fence
// lives with the action rather than only in whichever FE calls it.
func compactHandler(be *Backend, c *tool.Ctx) {
	src, set, ok := dataSource(be, c)
	if !ok {
		return
	}
	cp, ok := src.(Compactor)
	if !ok {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " cannot compact its store"})
		return
	}
	if !formBool(c, "confirm", false) {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error":   "compaction needs confirm=true",
			"warning": compactWarning,
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), compactTimeout)
	defer cancel()

	rep, err := cp.Compact(ctx, connFor(be, set))
	if err != nil {
		writeDataError(be, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"report": rep, "warning": compactWarning})
}

// ── shared plumbing ──────────────────────────────────────────────────

// dataSource resolves the backend's reader, answering 501 when it has none so
// an unimplemented backend is never mistaken for an empty store.
func dataSource(be *Backend, c *tool.Ctx) (DataSource, Settings, bool) {
	if be.Desc.Data == nil {
		c.JSON(http.StatusNotImplemented, map[string]string{"error": be.Desc.DisplayName + " exposes no panel data"})
		return nil, Settings{}, false
	}
	return be.Desc.Data, settingsFor(be.Desc.ID), true
}

// connFor resolves what the backend needs to reach its store.
//
// The panel always reads the daemon wick manages, never an instance's
// overridden server_url: the instance override exists so one agent can be
// pointed somewhere else, and following it here would make the dashboard
// describe a store that most instances are not using.
func connFor(be *Backend, set Settings) Conn {
	return Conn{
		BaseURL:    be.Mgr.BaseURL(),
		DataDir:    effectiveDataDir(be, set),
		WebEnabled: set.EnableWeb,
	}
}

// effectiveDataDir is the store the daemon was last told to use, falling back
// to the saved setting — so the panel measures the store actually in play
// rather than one saved but not yet started into.
func effectiveDataDir(be *Backend, set Settings) string {
	if d := strings.TrimSpace(be.Mgr.LaunchOpts().DataDir); d != "" {
		return d
	}
	return set.DataDir
}

// settingsFor reads the stored settings and applies the derived autostart
// lock. Every read goes through here so no caller sees a stored-but-wrong
// autostart value.
func settingsFor(id string) Settings {
	s, _ := settingsWithLock(id)
	return s
}

// settingsWithLock is settingsFor plus the lock it derived, for the callers
// that also report who holds it — one provider load instead of two.
func settingsWithLock(id string) (Settings, AutostartLock) {
	lock := AutostartLockFor(id)
	var s Settings
	if store != nil {
		s = store.Settings(id)
	}
	s.AutostartLocked = lock.Locked
	return s, lock
}

// settingsForUI is settingsWithLock with the bearer token replaced by a
// placeholder. Every response that carries settings goes through here: the
// token is a credential, and a panel that renders it puts it in a screenshot,
// a browser cache and a support thread. saveSettings recognises the
// placeholder and keeps the stored value (see tuningFromForm).
func settingsForUI(id string) (Settings, AutostartLock) {
	s, lock := settingsWithLock(id)
	s.AuthToken = maskSecret(s.AuthToken)
	return s, lock
}

// secretMask is what a set-but-hidden credential renders as. Its bullet is the
// marker saveSettings looks for, the same convention the provider forms use.
const secretMask = "••••••••"

func maskSecret(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return secretMask
}

// tuningFromForm reads the Settings tab's fields, falling back to the current
// value for anything the form did not send — so a partial post (or a future
// form that drops a field) cannot silently reset a daemon's configuration.
func tuningFromForm(c *tool.Ctx, cur Tuning) Tuning {
	t := Tuning{
		BasePath: strings.TrimSpace(c.Form("base_path")),
		LogLevel: strings.TrimSpace(c.Form("log_level")),

		AllowedHosts: strings.TrimSpace(c.Form("allowed_hosts")),
		AuthToken:    cur.AuthToken,

		CaptureMode:           strings.TrimSpace(c.Form("capture_mode")),
		ProjectStrategy:       strings.TrimSpace(c.Form("project_strategy")),
		CaptureAssistant:      formBool(c, "capture_assistant", cur.CaptureAssistant),
		NoCapturePrompts:      formBool(c, "no_capture_prompts", cur.NoCapturePrompts),
		SanitizeExtraPatterns: strings.TrimSpace(c.Form("sanitize_extra_patterns")),
		SanitizeAllowlist:     strings.TrimSpace(c.Form("sanitize_allowlist")),
		HookRatePerSec:        formFloat(c, "hook_rate_per_sec", cur.HookRatePerSec),
		HookRateBurst:         formFloat(c, "hook_rate_burst", cur.HookRateBurst),

		LLMProvider:                strings.TrimSpace(c.Form("llm_provider")),
		LLMModel:                   strings.TrimSpace(c.Form("llm_model")),
		EmbeddingProvider:          strings.TrimSpace(c.Form("embedding_provider")),
		EmbeddingModel:             strings.TrimSpace(c.Form("embedding_model")),
		EmbeddingDim:               formInt(c, "embedding_dim", cur.EmbeddingDim),
		MaxInputTokens:             formInt(c, "max_input_tokens", cur.MaxInputTokens),
		MaxOutputTokens:            formInt(c, "max_output_tokens", cur.MaxOutputTokens),
		AutoImproveRequireApproval: formBool(c, "auto_improve_require_approval", cur.AutoImproveRequireApproval),
		AutoImproveMinObservations: formInt(c, "auto_improve_min_observations", cur.AutoImproveMinObservations),
		AutoImproveMinConfidence:   formFloat(c, "auto_improve_min_confidence", cur.AutoImproveMinConfidence),
		AutoImproveMaxProposals:    formInt(c, "auto_improve_max_proposals_per_run", cur.AutoImproveMaxProposals),

		ObservationRetentionDays: formInt(c, "observation_retention_days", cur.ObservationRetentionDays),
		ObservationPruneBatch:    formInt(c, "observation_prune_batch", cur.ObservationPruneBatch),
		HardDeleteAfterDays:      formInt(c, "hard_delete_after_days", cur.HardDeleteAfterDays),
		ColdThreshold:            formFloat(c, "cold_threshold", cur.ColdThreshold),

		Reranker:        strings.TrimSpace(c.Form("reranker")),
		DecayLambda:     formFloat(c, "decay_lambda", cur.DecayLambda),
		DecaySigma:      formFloat(c, "decay_sigma", cur.DecaySigma),
		DecayMu:         formFloat(c, "decay_mu", cur.DecayMu),
		SalienceDefault: formFloat(c, "salience_default", cur.SalienceDefault),
		BreadthWeight:   formFloat(c, "breadth_weight", cur.BreadthWeight),

		BackfillAuto: formBool(c, "backfill_auto", cur.BackfillAuto),
	}
	// The token field arrives masked when it was not edited. Only an actual
	// new value replaces it; an empty submission clears it, which is how a
	// daemon goes back to running without auth.
	if raw := strings.TrimSpace(c.Form("auth_token")); !strings.Contains(raw, "•") {
		t.AuthToken = raw
	}
	return t
}

// scopeFromForm reads workspace/project/dir off the request. dir is checked
// for existence because the backend resolves an unknown directory to some
// OTHER project's scope instead of failing — a wrong answer that looks right
// (PLAN §14).
func scopeFromForm(c *tool.Ctx) (ReadScope, error) {
	s := ReadScope{
		Workspace: strings.TrimSpace(c.Form("workspace")),
		Project:   strings.TrimSpace(c.Form("project")),
		Dir:       strings.TrimSpace(c.Form("dir")),
	}
	if s.Dir != "" {
		fi, err := os.Stat(s.Dir)
		if err != nil || !fi.IsDir() {
			return ReadScope{}, errors.New("dir is not a readable directory: " + s.Dir)
		}
	}
	return s, nil
}

// writeDataError turns a backend read failure into a response the FE can
// branch on: a named reason plus the raw message. A disabled web API and a
// dead daemon are ordinary, fixable states, so they answer 200 with the reason
// rather than an error status the FE would render as a crash.
func writeDataError(be *Backend, c *tool.Ctx, err error) {
	reason := reasonFor(err, be.Mgr.Status(c.Context()))
	body := map[string]any{"error": err.Error(), "reason": reason}
	switch reason {
	case reasonWebDisabled, reasonNotRunning:
		body["hint"] = hintFor(reason)
		c.JSON(http.StatusOK, body)
	default:
		c.JSON(http.StatusBadGateway, body)
	}
}

// Reasons a panel read came back empty. They are the two states the user can
// actually fix, which is why they are named rather than left as prose.
const (
	reasonWebDisabled = "web_disabled"
	reasonNotRunning  = "daemon_not_running"
)

func reasonFor(err error, st Status) string {
	switch {
	case errors.Is(err, ErrWebDisabled):
		return reasonWebDisabled
	case !st.Running:
		return reasonNotRunning
	default:
		return ""
	}
}

func hintFor(reason string) string {
	switch reason {
	case reasonWebDisabled:
		return "Turn on \"web API\" in Agent Memory settings and restart the daemon — the project list and search are served by it."
	case reasonNotRunning:
		return "Start the Agent Memory daemon."
	}
	return ""
}

func formBool(c *tool.Ctx, key string, def bool) bool {
	switch strings.TrimSpace(c.Form(key)) {
	case "":
		return def
	case "true", "on", "1":
		return true
	default:
		return false
	}
}

func formInt(c *tool.Ctx, key string, def int) int {
	v := strings.TrimSpace(c.Form(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func formFloat(c *tool.Ctx, key string, def float64) float64 {
	v := strings.TrimSpace(c.Form(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// filepathIsAbs keeps the settings validation honest about what a store path
// means: the daemon is started from wick's own working directory, so a
// relative data_dir would resolve somewhere the user never typed.
func filepathIsAbs(p string) bool { return strings.HasPrefix(p, "/") }

// ── boot / shutdown hooks ────────────────────────────────────────────

// ApplySettings pushes the stored settings into every manager, so a port or
// store change made in the UI is what the next start uses. Called at
// registration, after a settings save, and before any start.
func ApplySettings() {
	if store == nil {
		return
	}
	for _, be := range List() {
		s := store.Settings(be.Desc.ID)
		be.Mgr.SetPrefPort(s.Port)
		// The token is decrypted here, at the last moment before it becomes
		// a launch option — it is stored encrypted and never leaves this
		// package in plaintext otherwise.
		be.Mgr.SetLaunchOptions(LaunchOptions{
			DataDir:   s.DataDir,
			EnableWeb: s.EnableWeb,
			AuthToken: resolveSecret(s.AuthToken),
			Tuning:    s.Tuning,
		})
	}
}

// Autostart starts every backend whose autostart is effectively on — stored,
// or forced by a provider instance that uses it. Safe to call in a goroutine
// at boot.
func Autostart(logf func(string)) {
	if store == nil || !store.Enabled() {
		return
	}
	ApplySettings()
	for _, be := range List() {
		if !settingsFor(be.Desc.ID).EffectiveAutostart() {
			continue
		}
		if !be.Mgr.Installed() {
			if logf != nil {
				logf("agentmemory: " + be.Desc.ID + " autostart on but not installed — skipping")
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		if err := be.Mgr.StartAndWait(ctx); err != nil && logf != nil {
			logf("agentmemory: " + be.Desc.ID + " autostart failed: " + err.Error())
		}
		cancel()
	}
}

// AnyAutostartEnabled reports whether at least one backend will start at boot,
// so server.go can decide whether to add a boot-gate step.
func AnyAutostartEnabled() bool {
	if store == nil || !store.Enabled() {
		return false
	}
	for _, be := range List() {
		if settingsFor(be.Desc.ID).EffectiveAutostart() {
			return true
		}
	}
	return false
}

// StopAll kills every running daemon. Exposed for shutdown hooks.
func StopAll() {
	for _, be := range List() {
		be.Mgr.StopProcess()
	}
}
