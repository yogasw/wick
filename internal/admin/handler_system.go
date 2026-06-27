package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/updater"
	"github.com/yogasw/wick/internal/userconfig"
)

// systemUpdateSSEKeepalive is how often the status stream emits a
// comment frame so reverse proxies don't reap an idle connection while
// the user sits on the System page between actions.
const systemUpdateSSEKeepalive = 15 * time.Second

// systemPage renders the System config page: current/latest version,
// staged-update state, the auto-update toggle, and the update controls.
// Degrades to a "not configured" notice when no release source is set.
func (h *Handler) systemPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())

	dbType, dbStatus := h.dbInfo()
	vm := view.SystemPageData{
		AppName:     h.configs.AppName(),
		WickVersion: h.sys.WickVersion,
		Commit:      h.sys.Commit,
		BuildTime:   h.sys.BuildTime,
		AccessType:  "http", // the admin page is always reached over HTTP
		DBType:      dbType,
		DBStatus:    dbStatus,
	}
	if cfg, err := userconfig.Load(h.sys.AppName); err == nil {
		vm.AutoUpdate = cfg.AutoUpdate
	}
	if h.sys.Coordinator != nil {
		st := h.sys.Coordinator.Snapshot()
		vm.CurrentVersion = st.CurrentVersion
		vm.LatestVersion = st.LatestVersion
		vm.HasStaged = st.HasStaged
		vm.StagedVersion = st.StagedVersion
		vm.Phase = string(st.Phase)
		vm.Percent = st.Percent
		vm.ReleaseNotes = st.ReleaseNotes
		vm.PublishedAt = st.PublishedAt
		vm.WantedAsset = st.WantedAsset
		vm.Error = st.Error
		if upd := h.sys.Coordinator.Updater(); upd != nil {
			vm.Configured = upd.Configured()
			vm.IsOfficial = upd.IsOfficial()
			vm.ChangelogURL = upd.ChangelogURL()
		}
	}
	// Wick framework update state from the background cache — no live
	// request on page load. On official builds the app fields already
	// carry the framework's state (app == framework), so this only feeds
	// the non-official wick row + "What's new" block.
	if h.sys.VersionCache != nil {
		snap := h.sys.VersionCache.Snapshot()
		vm.WickUpdateKnown = snap.WickUpdateKnown
		vm.WickUpdate = snap.WickUpdate
		vm.WickLatest = snap.WickLatest
		vm.WickNotes = snap.WickNotes
		vm.WickPublishedAt = snap.WickPublishedAt
		vm.WickChangelogURL = snap.WickChangelogURL
	}
	view.SystemPage(vm, user).Render(r.Context(), w)
}

// systemUpdateStatus streams the coordinator's Status as SSE so the page
// can render a live download-progress bar. One frame on connect, one per
// status change, plus periodic keepalives. The connection ends when the
// client disconnects (r.Context cancelled) or — during an apply — when
// the process re-execs and the socket drops.
func (h *Handler) systemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if h.sys.Coordinator == nil {
		http.Error(w, "updater not available", http.StatusServiceUnavailable)
		return
	}
	// Set the SSE headers BEFORE the first WriteHeader/Flush. The previous
	// order flushed first (committing a 200 with the default text/plain
	// content-type) and only then set Content-Type — so the browser saw a
	// non-SSE response and never processed events live until a full reload
	// re-fetched the snapshot. curl ignores content-type so it masked the
	// bug. The status-capturing middleware wraps the writer with Unwrap but
	// no Flush, so http.NewResponseController is used to reach the real
	// flusher one layer down (mirrors internal/mcp/sse.go).
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		// Header already committed; nothing useful to send. Just bail so a
		// non-flushable writer doesn't hang the client on a dead stream.
		return
	}

	ch, unsub := h.sys.Coordinator.Subscribe()
	defer unsub()

	keepalive := time.NewTicker(systemUpdateSSEKeepalive)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case st, ok := <-ch:
			if !ok {
				return
			}
			body, err := json.Marshal(st)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", body); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

// systemUpdateCheck kicks off a check+download in the background. Returns
// 202 immediately; progress flows over the SSE status stream. A second
// check while one is in flight is a no-op inside the coordinator.
func (h *Handler) systemUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if h.sys.Coordinator == nil || h.sys.Coordinator.Updater() == nil || !h.sys.Coordinator.Updater().Configured() {
		http.Error(w, "updater not configured", http.StatusBadRequest)
		return
	}
	// Detach from the request: the check outlives this short POST and
	// reports via SSE. A fresh background context (not r.Context) so the
	// download isn't cancelled when the POST response is written.
	go h.sys.Coordinator.Check(context.Background())
	w.WriteHeader(http.StatusAccepted)
}

// systemUpdateApply applies the staged update, reusing the exact same
// updater.ApplyStagedAndRestart path the tray uses — it already picks the
// right per-OS swap (MSI helper on Windows, syscall.Exec in place on
// Linux/macOS). Web and tray run in one process, so there is no separate
// "web" jalur: whether launched as a tray or a headless `all` daemon, the
// relaunch behaviour follows the binary's own install/run mode.
//
// It responds first (so the browser can begin polling /health), then
// fires the swap in a goroutine. On success ApplyStagedAndRestart never
// returns — the process image is replaced (Unix) or the process exits and
// a helper relaunches it (Windows MSI). The browser's /health poll then
// reloads once the app re-serves per its own autostart config.
func (h *Handler) systemUpdateApply(w http.ResponseWriter, r *http.Request) {
	upd := (*updater.Updater)(nil)
	if h.sys.Coordinator != nil {
		upd = h.sys.Coordinator.Updater()
	}
	if upd == nil || !upd.HasStaged() {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "no staged update to apply"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true})
	// Flush so the client gets the response before we tear down the
	// server below. Unwrap-aware to see through the status middleware.
	_ = http.NewResponseController(w).Flush()

	go func() {
		h.sys.Coordinator.MarkApplying()
		// Give the HTTP response a beat to reach the client before the
		// listener is cancelled by the stop func below.
		time.Sleep(300 * time.Millisecond)
		// processctl.StopServer drains the in-process server when tray-
		// managed (no-op otherwise). The real teardown comes from the swap
		// itself. Logging only — on success the next line never runs.
		if err := upd.ApplyStagedAndRestart(func() { _ = processctl.StopServer() }); err != nil {
			log.Error().Err(err).Msg("apply staged update: re-exec failed — continuing on current binary")
		}
	}()
}

// systemSetAutoUpdate persists the auto-update toggle into userconfig.
func (h *Handler) systemSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	enabled := boolParam(r, "enabled")
	cfg, err := userconfig.Load(h.sys.AppName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg.AutoUpdate = enabled
	if err := userconfig.Save(h.sys.AppName, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/advanced/software-update", http.StatusFound)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
