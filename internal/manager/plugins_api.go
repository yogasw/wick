package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gorm.io/gorm"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/login"
	pluginreplace "github.com/yogasw/wick/internal/plugins/replace"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// PluginsHandler serves the connector-plugin marketplace surface for the
// manager SPA: the merged Installed + Available list, plus install / enable /
// disable / remove actions. It is a small self-contained handler (its own DB +
// registry) wired directly in server.go, so the main manager.Handler signature
// stays untouched. All routes are admin-gated — installing native code is a
// privileged action.
// reconciler is the slice of *connplugin.Reloader this handler needs: trigger
// an immediate scan so an install/enable/disable/remove takes effect right away
// instead of waiting for the poll loop (or never, if no plugins existed at boot).
type reconciler interface {
	Reload(ctx context.Context)
}

type PluginsHandler struct {
	store    *connplugin.StateStore
	registry *connplugin.Catalog
	dir      string
	reloader reconciler // nil when plugins are disabled; reload() is a no-op then
	// sources updates plugins that were installed from a plugin source
	// (url / GitHub) from that source instead of the connector catalog.
	sources *PluginSourcesHandler
	// replacer re-runs the replaces migration on demand; refresh reloads
	// the configs cache for the new key afterwards (configs.EnsureOwned).
	replacer *pluginreplace.Migrator
	refresh  func(ctx context.Context, owner string, rows ...pkgentity.Config) error
	// unload stops a plugin before its files go: a service's supervised
	// process, a job's schedule, a tool's runner. nil = nothing to stop.
	unload func(ctx context.Context, kind, key string)
	// builtin reports whether key is a built-in (non-plugin) module, which
	// Uninstall refuses instead of answering "not installed".
	builtin func(key string) bool
}

// SetUninstall wires what Uninstall needs beyond deleting files: unload
// stops the running plugin first, builtin guards built-in keys.
func (h *PluginsHandler) SetUninstall(unload func(ctx context.Context, kind, key string), builtin func(key string) bool) *PluginsHandler {
	h.unload, h.builtin = unload, builtin
	return h
}

// SetReplaceRefresh wires the configs-cache refresh the replace endpoint
// calls after an apply, so copied values are live without a restart.
func (h *PluginsHandler) SetReplaceRefresh(fn func(ctx context.Context, owner string, rows ...pkgentity.Config) error) *PluginsHandler {
	h.refresh = fn
	return h
}

// SetSources routes updates of source-installed plugins to their source.
func (h *PluginsHandler) SetSources(s *PluginSourcesHandler) *PluginsHandler {
	h.sources = s
	return h
}

// NewPluginsHandler builds the marketplace handler. db backs the enable/disable
// overlay; dir is the installed-plugins directory (connplugin.DefaultDir()).
func NewPluginsHandler(db *gorm.DB) *PluginsHandler {
	return &PluginsHandler{
		store:    connplugin.NewStateStore(db),
		registry: connplugin.DefaultRegistry(),
		dir:      connplugin.DefaultDir(),
		replacer: pluginreplace.New(db),
	}
}

// SetReloader wires the hot-reload poller so state-changing actions reconcile
// immediately. Safe to leave unset — reload() then does nothing.
func (h *PluginsHandler) SetReloader(r reconciler) *PluginsHandler {
	h.reloader = r
	return h
}

// reload triggers an immediate reconcile so a freshly installed/enabled plugin
// registers into the connectors service now, not after the next poll tick.
func (h *PluginsHandler) reload(ctx context.Context) {
	if h.reloader != nil {
		h.reloader.Reload(ctx)
	}
}

// RegisterRoutes wires the marketplace endpoints under /manager/api/plugins.
// Listing is readable by any logged-in user (mirrors built-in connectors:
// everyone can browse, only admins can act). The state-changing actions —
// install / update / enable / disable / remove — stay admin-only.
func (h *PluginsHandler) RegisterRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAuth(next)
	}
	admin := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAdmin(next)
	}
	mux.Handle("GET /manager/api/plugins", auth(h.apiList))
	mux.Handle("GET /manager/api/plugins/installed", auth(h.apiInstalled))
	mux.Handle("POST /manager/api/plugins/install", admin(h.apiInstall))
	mux.Handle("POST /manager/api/plugins/{key}/update", admin(h.apiUpdate))
	mux.Handle("POST /manager/api/plugins/{key}/enable", admin(h.apiEnable))
	mux.Handle("POST /manager/api/plugins/{key}/disable", admin(h.apiDisable))
	mux.Handle("POST /manager/api/plugins/{key}/remove", admin(h.apiRemove))
	mux.Handle("GET /manager/api/plugins/{key}/replace", admin(h.apiReplacePlan))
	mux.Handle("POST /manager/api/plugins/{key}/replace", admin(h.apiReplaceApply))
}

// pluginEntry is the JSON shape the SPA renders. installed=false entries come
// from the marketplace registry (Available); installed=true from the local scan.
type pluginEntry struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Installed   bool     `json:"installed"`
	Enabled     bool     `json:"enabled"`
	ArchOK      bool     `json:"arch_ok"`
	Host        string   `json:"host"`     // this server's os/arch, for "no build for X" copy
	OSArch      []string `json:"os_arch"`  // os/arch the plugin ships a build for
	Category    string   `json:"category"` // derived from DefaultTags via connectorCategory, like built-ins
	Signed      string   `json:"signed"`   // none | valid | INVALID
	// UpdateAvailable is set on installed entries when the catalog carries a
	// newer version than the one on disk; LatestVersion is that catalog version.
	UpdateAvailable bool   `json:"update_available,omitempty"`
	LatestVersion   string `json:"latest_version,omitempty"`
}

type pluginsListResponse struct {
	Installed []pluginEntry `json:"installed"`
	Available []pluginEntry `json:"available"`
	// Catalog is every catalog entry, installed or not (Installed set on the
	// ones already on disk) — the Marketplace lists the whole catalog, while
	// Available keeps meaning "not installed yet" for the connector pages.
	Catalog []pluginEntry `json:"catalog"`
	// RegistryError is set (and Available empty) when the marketplace fetch
	// failed — the SPA shows installed plugins regardless and surfaces this.
	RegistryError string `json:"registry_error,omitempty"`
	// IsAdmin tells the SPA whether the viewer may act (install / update /
	// enable / disable / remove). Non-admins still get the full list but the
	// action buttons render disabled with a "requires admin" hint.
	IsAdmin bool `json:"is_admin"`
}

func (h *PluginsHandler) apiList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	host := runtime.GOOS + "/" + runtime.GOARCH

	user := login.GetUser(ctx)
	resp := pluginsListResponse{
		Installed: []pluginEntry{},
		Available: []pluginEntry{},
		Catalog:   []pluginEntry{},
		IsAdmin:   user != nil && user.IsAdmin(),
	}

	// Catalog fetched once up front: used both to flag updates on installed
	// plugins and to build the Available list. A fetch error is non-fatal —
	// installed plugins still render, just without an update hint.
	avail, regErr := h.registry.List(ctx)
	if regErr != nil {
		resp.RegistryError = regErr.Error()
	}
	catalogByKey := make(map[string]connplugin.Available, len(avail))
	for _, a := range avail {
		catalogByKey[a.Key] = a
	}

	// Installed: scan the plugins dir + overlay enable state.
	found, _ := connplugin.Scan(h.dir)
	states, _ := h.store.List()
	installedKeys := map[string]bool{}
	for _, f := range found {
		enabled := true
		if v, ok := states[f.Key]; ok {
			enabled = v
		}
		archOK := false
		for _, a := range f.Manifest.OSArch {
			if a == host {
				archOK = true
			}
		}
		signed := "none"
		if f.Manifest.Signature != "" {
			if wickplugin.VerifySHA256(wickplugin.TrustedKeys(), f.Manifest.SHA256, f.Manifest.Signature) {
				signed = "valid"
			} else {
				signed = "INVALID"
			}
		}
		installedKeys[f.Key] = true
		cat, _, _ := connectorCategory(f.Manifest.Module.Meta.DefaultTags, false)
		entry := pluginEntry{
			Key:         f.Key,
			Name:        f.Manifest.Module.Meta.Name,
			Description: f.Manifest.Module.Meta.Description,
			Version:     f.Manifest.Version,
			Installed:   true,
			Enabled:     enabled,
			ArchOK:      archOK,
			Category:    cat,
			Signed:      signed,
		}
		// Flag an update when the catalog carries a newer version than disk.
		if c, ok := catalogByKey[f.Key]; ok && connplugin.VersionNewer(c.Version, f.Manifest.Version) {
			entry.UpdateAvailable = true
			entry.LatestVersion = c.Version
		}
		resp.Installed = append(resp.Installed, entry)
	}

	// Catalog: every entry; Available: the ones not already installed.
	for _, a := range avail {
		osArch := make([]string, 0, len(a.Assets))
		for oa := range a.Assets {
			osArch = append(osArch, oa)
		}
		sort.Strings(osArch)
		// Same connectorCategory() built-ins use — Available.DefaultTags is
		// the identical []entity.DefaultTag (= tool.DefaultTag) type.
		cat, _, _ := connectorCategory(a.DefaultTags, false)
		entry := pluginEntry{
			Key:         a.Key,
			Name:        a.Name,
			Description: a.Description,
			Version:     a.Version,
			Installed:   installedKeys[a.Key],
			ArchOK:      a.AssetFor(host) != "",
			Host:        host,
			OSArch:      osArch,
			Category:    cat,
		}
		resp.Catalog = append(resp.Catalog, entry)
		if !entry.Installed {
			resp.Available = append(resp.Available, entry)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type installRequest struct {
	Name string `json:"name"`
}

func (h *PluginsHandler) apiInstall(w http.ResponseWriter, r *http.Request) {
	var req installRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	avail, url, err := h.registry.Resolve(ctx, req.Name, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Register it into the connectors service now so it appears in the
	// connector list / manager / admin immediately, not after the poll tick.
	h.installOrUpdate(w, r, key(req.Name), url, avail.Version, map[string]any{"ok": true, "installed": req.Name})
}

// apiUpdate re-downloads the latest catalog version for an already-installed
// plugin, replacing its binary + manifest, then reconciles.
//
// Ordering guarantees the "download-succeeds-then-replace" contract: the new
// archive is fetched and verified into a temp dir first, and only the Replacing
// phase touches the on-disk plugin (via an atomic rename that survives the old
// binary still running — see connplugin.InstallFromURLProgress / copyFile). The
// reconcile after replace kills the stale subprocess so the next call spawns the
// new binary.
func (h *PluginsHandler) apiUpdate(w http.ResponseWriter, r *http.Request) {
	k := r.PathValue("key")
	if k == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if h.sources.updateFromSource(w, r, k) {
		return
	}
	ctx := r.Context()
	avail, url, err := h.registry.Resolve(ctx, k, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.installOrUpdate(w, r, k, url, avail.Version, map[string]any{"ok": true, "updated": k, "version": avail.Version})
}

// key normalizes a marketplace name to a plugin key for progress labelling.
// The catalog name is already the key for our plugins; kept as a seam in case
// they diverge.
func key(name string) string { return name }

// installOrUpdate runs the download→verify→replace→reconcile pipeline. When the
// client sent `Accept: text/event-stream` it streams staged progress as SSE
// (`data: {phase,pct}` frames, then a terminal `data: {ok|error}`); otherwise it
// blocks and returns the plain JSON `done` payload (backward-compatible with the
// CLI and any non-SSE caller).
func (h *PluginsHandler) installOrUpdate(w http.ResponseWriter, r *http.Request, k, url, version string, done map[string]any) {
	ctx := r.Context()
	if !wantsSSE(r) {
		if err := connplugin.InstallFromURL(ctx, url, h.dir); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.recordOfficial(k, version)
		h.reload(ctx)
		writeJSON(w, http.StatusOK, done)
		return
	}

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()

	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		_ = rc.Flush()
	}

	// The progress callback fires from the download goroutine (io.Copy on this
	// same goroutine, actually), so writes are serialized — no extra locking.
	err := connplugin.InstallFromURLProgress(ctx, url, h.dir, func(p connplugin.Progress) {
		send(p)
	})
	if err != nil {
		send(map[string]any{"phase": "error", "error": err.Error()})
		return
	}
	h.recordOfficial(k, version)
	h.reload(ctx)
	send(done)
}

// recordOfficial marks k as installed from the official wick catalog (the
// only thing installOrUpdate installs from), so Admin → Plugins can show
// its origin. The catalog only carries connectors.
func (h *PluginsHandler) recordOfficial(k, version string) {
	_ = h.store.Record(k, wickplugin.KindConnector, version)
	_ = h.store.SetOrigin(k, connplugin.OriginOfficial)
}

// wantsSSE reports whether the caller opted into a Server-Sent Events response.
func wantsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

func (h *PluginsHandler) apiEnable(w http.ResponseWriter, r *http.Request) { h.setEnabled(w, r, true) }
func (h *PluginsHandler) apiDisable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *PluginsHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if err := h.store.SetEnabled(key, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Reconcile so the connector appears (enable) or vanishes (disable) now.
	h.reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "enabled": enabled})
}

// apiRemove uninstalls plugin {key} of any kind. The kind folders are
// scanned like the Installed list does; ?kind= picks one when a connector and
// a tool share a key. The running plugin is unloaded first (a service is
// stopped before its files go), then the folder is deleted, the recorded
// version is cleared and connectors reconcile. Config rows are kept, so a
// reinstall picks them up again.
func (h *PluginsHandler) apiRemove(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	want := r.URL.Query().Get("kind")
	for _, kind := range wickplugin.Kinds {
		if want != "" && wickplugin.NormalizeKind(want) != kind {
			continue
		}
		dir := connplugin.KindDir(kind)
		if kind == wickplugin.KindConnector {
			dir = h.dir
		}
		found, err := connplugin.ScanKind(dir, kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, f := range found {
			if f.Key != key {
				continue
			}
			if h.unload != nil {
				h.unload(r.Context(), kind, key)
			}
			if err := os.RemoveAll(filepath.Dir(f.BinaryPath)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = h.store.ClearInstalled(key)
			// Reconcile so a connector drops out of the lists now.
			h.reload(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": key, "kind": kind})
			return
		}
	}
	if h.builtin != nil && h.builtin(key) {
		http.Error(w, "built-in, not a plugin: it cannot be uninstalled", http.StatusConflict)
		return
	}
	http.Error(w, "plugin not installed", http.StatusNotFound)
}

// apiReplacePlan is the dry run of the replaces migration for plugin {key}:
// per old key, which config fields would move (secrets as "set"/"empty"),
// the job settings and the access merge. Nothing is written.
func (h *PluginsHandler) apiReplacePlan(w http.ResponseWriter, r *http.Request) {
	h.replace(w, r, false)
}

// apiReplaceApply re-runs the migration. A pair migrated before is skipped
// unless ?force=1; manual values on the plugin are never overwritten.
func (h *PluginsHandler) apiReplaceApply(w http.ResponseWriter, r *http.Request) {
	h.replace(w, r, true)
}

func (h *PluginsHandler) replace(w http.ResponseWriter, r *http.Request, apply bool) {
	key := r.PathValue("key")
	pairs := pluginreplace.Lookup(key)
	if len(pairs) == 0 {
		http.Error(w, "plugin "+key+" replaces nothing (or is not loaded)", http.StatusNotFound)
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	reports := make([]pluginreplace.Report, 0, len(pairs))
	for _, p := range pairs {
		var rep pluginreplace.Report
		var err error
		if apply {
			rep, err = h.replacer.Apply(r.Context(), p, actor(r), force)
			if err == nil && !rep.AlreadyDone && h.refresh != nil {
				err = h.refresh(r.Context(), p.New, p.Configs...)
			}
		} else {
			rep, err = h.replacer.Plan(r.Context(), p)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reports = append(reports, rep)
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "applied": apply, "reports": reports})
}
