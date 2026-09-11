package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/upgrade"
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
		// First paint of the live state the script then keeps fresh: which
		// process is answering, whether it can hand over, and what it is
		// still doing.
		ServingPID: os.Getpid(),
		Graceful:   upgrade.Armed(),
		Busy:       upgrade.Busy(),
		Settle:     upgrade.DrainQuiet().String(),
	}
	if sw := h.pendingSwap(); sw.Pending {
		vm.SwapPending = true
		vm.SwapFrom, vm.SwapTo, vm.SwapSource, vm.SwapBuilt = sw.From, sw.To, sw.Source, sw.Built
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

	// force=1: the operator saw what was running and chose not to wait for
	// it. Set BEFORE the handover starts — once this process is draining it
	// no longer answers HTTP, so nothing can reach it to change its mind.
	forced := boolParam(r, "force")
	if forced {
		busy := upgrade.Busy()
		log.Warn().Bool("forced", true).Strs("interrupting", busy).
			Msg("apply staged update: forced swap requested — running work will be cut off")
		upgrade.ForceDrain()
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart": true, "forced": forced})
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

// systemServingInfo answers the two questions the System page cannot answer
// from its own HTML: WHICH process is serving me, and what is it still doing?
//
// The pid is how the page knows a handover completed. It used to infer that
// from /health going down and coming back — which is exactly what a
// zero-downtime upgrade never does, so the page sat on "Restarting…" until it
// gave up. A pid it can compare is the same answer, from wick, without
// waiting for an outage that is not coming.
//
// The busy list is what a Force swap would interrupt, named. Nobody should be
// asked to confirm "cut off running work?" without being shown the work.
func (h *Handler) systemServingInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.servingPayload())
}

// systemServingStream pushes the same payload every second over SSE, so the
// countdowns move in real time instead of jumping between polls, and a swap
// that completes is reflected the moment it happens.
//
// SSE rather than a websocket: this is one-way, the page already speaks it
// (the updater status stream), and it needs no upgrade handshake through
// whatever proxy sits in front. Like any stream it excludes itself from the
// drain's in-flight count, so watching this page can never hold a handover
// open — the bug this whole feature started with.
func (h *Handler) systemServingStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return
	}
	send := func() bool {
		body, err := json.Marshal(h.servingPayload())
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: serving\ndata: %s\n\n", body); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send() {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if !send() {
				return
			}
		}
	}
}

// servingPayload is the one description of "which process is answering, what
// is it doing, and what is waiting to replace it" — shared by the one-shot
// GET and the stream so the two can never drift.
func (h *Handler) servingPayload() map[string]any {
	version := h.sys.WickVersion
	if h.sys.Coordinator != nil {
		if st := h.sys.Coordinator.Snapshot(); st.CurrentVersion != "" {
			version = st.CurrentVersion
		}
	}
	body := map[string]any{
		"pid":       os.Getpid(),
		"version":   version,
		"graceful":  upgrade.Armed(),
		"forced":    upgrade.Forced(),
		"settle":    upgrade.DrainQuiet().String(),
		"busy":      upgrade.Busy(),
		// The same thing in words, for the panel and the confirm dialog.
		"busy_label": upgrade.BusyHuman(),
		"swap":      h.pendingSwap(),
		"auto_swap": upgrade.AutoSwapStatus(),
		// How long a boot took here last time, so the UI can say "usually
		// about this long" instead of leaving a minute of silence unexplained.
		"typical_boot_seconds": upgrade.TypicalBootSeconds(),
		"inherited": upgrade.Inherited(),
	}
	if since := upgrade.ServingSince(); !since.IsZero() {
		body["serving_since"] = since.UTC().Format(time.RFC3339)
	}
	// A failed handover is invisible from the outside: the old process simply
	// keeps serving, which looks identical to nobody having tried. Say so.
	if at, ok, msg := upgrade.LastHandover(); !at.IsZero() {
		body["last_handover"] = map[string]any{
			"at":    at.UTC().Format(time.RFC3339),
			"ok":    ok,
			"error": msg,
		}
	}
	return body
}

// pendingSwap reports a build that is installed or downloaded but not yet
// running: a binary on disk this process is not the image of, or a staged
// update nobody has applied. Either way the operator wants the same two
// facts — which version would replace which, and what the handover is still
// waiting for.
func (h *Handler) pendingSwap() pendingSwap {
	if sw := detectPendingSwap(h.sys.AppVersion, h.sys.BuildTime); sw.Pending {
		return sw
	}
	// Nothing on disk, but the updater may be holding a download.
	if h.sys.Coordinator != nil {
		if st := h.sys.Coordinator.Snapshot(); st.HasStaged && st.StagedVersion != "" {
			return pendingSwap{
				Pending: true,
				From:    h.sys.AppVersion,
				To:      st.StagedVersion,
				Source:  "staged update (not applied yet)",
			}
		}
	}
	return pendingSwap{}
}

// systemSwapNow hands over to the binary already installed at the daemon's
// exec path — the state the CLI leaves behind when a build is installed but
// not reloaded. It is the same handover SIGHUP starts; no file is touched
// here, because the file is already in place.
//
// force=1 additionally tells the outgoing process not to wait for its
// in-flight work. That is a human decision, made while looking at the list
// the page shows, so it is a separate parameter rather than a timeout.
func (h *Handler) systemSwapNow(w http.ResponseWriter, r *http.Request) {
	if !upgrade.Armed() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "graceful handover is not available in this process — restart the service instead",
		})
		return
	}
	forced := boolParam(r, "force")
	busy := upgrade.Busy()
	if forced {
		log.Warn().Strs("interrupting", busy).
			Msg("swap now: forced — running work will be cut off")
		upgrade.ForceDrain()
	} else {
		log.Info().Strs("outstanding", busy).Msg("swap now: handover requested from the admin UI")
	}
	if err := upgrade.Trigger(); err != nil {
		// A handover already underway is not a failure of this request — and
		// when force was asked for, the flag above is already set, so the
		// drain will not wait even though no NEW successor could be started.
		// Reporting "could not start the handover" here told the operator
		// their click did nothing, which was the opposite of the truth.
		msg := err.Error()
		if strings.Contains(msg, "upgrade in progress") || strings.Contains(msg, "parent hasn't exited") {
			note := "A handover is already underway — nothing new to start."
			if forced {
				note = "A handover is already underway; it will now hand over without waiting for the work above."
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "forced": forced, "already": true, "message": note,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": msg})
		return
	}
	// Same phase the watcher publishes, so the page shows "handing over — the
	// new process is booting" for the ~90s that takes, whether the handover
	// was started by a click or by the watcher.
	to := ""
	if sw := h.pendingSwap(); sw.Pending {
		to = sw.To
	}
	upgrade.SetAutoSwap(upgrade.AutoSwap{State: "handing_over", To: to})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "forced": forced})
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
