package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"gorm.io/gorm"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/login"
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
}

// NewPluginsHandler builds the marketplace handler. db backs the enable/disable
// overlay; dir is the installed-plugins directory (connplugin.DefaultDir()).
func NewPluginsHandler(db *gorm.DB) *PluginsHandler {
	return &PluginsHandler{
		store:    connplugin.NewStateStore(db),
		registry: connplugin.DefaultRegistry(),
		dir:      connplugin.DefaultDir(),
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
// Every route is admin-only.
func (h *PluginsHandler) RegisterRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	admin := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAdmin(next)
	}
	mux.Handle("GET /manager/api/plugins", admin(h.apiList))
	mux.Handle("POST /manager/api/plugins/install", admin(h.apiInstall))
	mux.Handle("POST /manager/api/plugins/{key}/update", admin(h.apiUpdate))
	mux.Handle("POST /manager/api/plugins/{key}/enable", admin(h.apiEnable))
	mux.Handle("POST /manager/api/plugins/{key}/disable", admin(h.apiDisable))
	mux.Handle("POST /manager/api/plugins/{key}/remove", admin(h.apiRemove))
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
	// RegistryError is set (and Available empty) when the marketplace fetch
	// failed — the SPA shows installed plugins regardless and surfaces this.
	RegistryError string `json:"registry_error,omitempty"`
}

func (h *PluginsHandler) apiList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	host := runtime.GOOS + "/" + runtime.GOARCH

	resp := pluginsListResponse{Installed: []pluginEntry{}, Available: []pluginEntry{}}

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

	// Available: catalog entries not already installed.
	for _, a := range avail {
		if installedKeys[a.Key] {
			continue
		}
		osArch := make([]string, 0, len(a.Assets))
		for oa := range a.Assets {
			osArch = append(osArch, oa)
		}
		sort.Strings(osArch)
		// Same connectorCategory() built-ins use — Available.DefaultTags is
		// the identical []entity.DefaultTag (= tool.DefaultTag) type.
		cat, _, _ := connectorCategory(a.DefaultTags, false)
		resp.Available = append(resp.Available, pluginEntry{
			Key:         a.Key,
			Name:        a.Name,
			Description: a.Description,
			Version:     a.Version,
			Installed:   false,
			ArchOK:      a.AssetFor(host) != "",
			Host:        host,
			OSArch:      osArch,
			Category:    cat,
		})
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
	_, url, err := h.registry.Resolve(ctx, req.Name, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := connplugin.InstallFromURL(ctx, url, h.dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Register it into the connectors service now so it appears in the
	// connector list / manager / admin immediately, not after the poll tick.
	h.reload(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "installed": req.Name})
}

// apiUpdate re-downloads the latest catalog version for an already-installed
// plugin, overwriting its dir in place, then reconciles. Same path as install
// (InstallFromURL replaces the existing files); keyed by {key} from the URL.
func (h *PluginsHandler) apiUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	avail, url, err := h.registry.Resolve(ctx, key, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := connplugin.InstallFromURL(ctx, url, h.dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.reload(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": key, "version": avail.Version})
}

func (h *PluginsHandler) apiEnable(w http.ResponseWriter, r *http.Request)  { h.setEnabled(w, r, true) }
func (h *PluginsHandler) apiDisable(w http.ResponseWriter, r *http.Request) { h.setEnabled(w, r, false) }

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

func (h *PluginsHandler) apiRemove(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	found, err := connplugin.Scan(h.dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range found {
		if f.Key == key {
			if err := os.RemoveAll(filepath.Dir(f.BinaryPath)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Reconcile so the connector drops out of the lists now.
			h.reload(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": key})
			return
		}
	}
	http.Error(w, "plugin not installed", http.StatusNotFound)
}
