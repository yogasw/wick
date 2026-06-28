package manager

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

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
type PluginsHandler struct {
	store    *connplugin.StateStore
	registry *connplugin.Catalog
	dir      string
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

// RegisterRoutes wires the marketplace endpoints under /manager/api/plugins.
// Every route is admin-only.
func (h *PluginsHandler) RegisterRoutes(mux *http.ServeMux, authMidd *login.Middleware) {
	admin := func(next http.HandlerFunc) http.Handler {
		return authMidd.RequireAdmin(next)
	}
	mux.Handle("GET /manager/api/plugins", admin(h.apiList))
	mux.Handle("POST /manager/api/plugins/install", admin(h.apiInstall))
	mux.Handle("POST /manager/api/plugins/{key}/enable", admin(h.apiEnable))
	mux.Handle("POST /manager/api/plugins/{key}/disable", admin(h.apiDisable))
	mux.Handle("POST /manager/api/plugins/{key}/remove", admin(h.apiRemove))
}

// pluginEntry is the JSON shape the SPA renders. installed=false entries come
// from the marketplace registry (Available); installed=true from the local scan.
type pluginEntry struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	ArchOK      bool   `json:"arch_ok"`
	Signed      string `json:"signed"` // none | valid | INVALID
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
		resp.Installed = append(resp.Installed, pluginEntry{
			Key:         f.Key,
			Name:        f.Manifest.Module.Meta.Name,
			Description: f.Manifest.Module.Meta.Description,
			Version:     f.Manifest.Version,
			Installed:   true,
			Enabled:     enabled,
			ArchOK:      archOK,
			Signed:      signed,
		})
	}

	// Available: registry entries not already installed.
	avail, err := h.registry.List(ctx)
	if err != nil {
		resp.RegistryError = err.Error()
	} else {
		for _, a := range avail {
			if installedKeys[a.Key] {
				continue
			}
			resp.Available = append(resp.Available, pluginEntry{
				Key:         a.Key,
				Name:        a.Name,
				Description: a.Description,
				Version:     a.Version,
				Installed:   false,
				ArchOK:      a.AssetFor(host) != "",
			})
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
	_, url, err := h.registry.Resolve(ctx, req.Name, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := connplugin.InstallFromURL(ctx, url, h.dir); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "installed": req.Name})
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
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": key})
			return
		}
	}
	http.Error(w, "plugin not installed", http.StatusNotFound)
}
