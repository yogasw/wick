package admin

import (
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
)

// auditPage hosts the manager SPA's audit views inside the admin chrome,
// the same way pluginsPage hosts the plugins view: what ran (Code Mode
// scripts, connector calls), by whom, where, and whether it failed. The
// JSON stays at /manager/api/runs and /manager/api/codemode-runs.
func (h *Handler) auditPage(w http.ResponseWriter, r *http.Request) {
	assetURL, base := pluginsSPAMount()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = view.AuditPage(view.PluginsSPAVM{
		AssetURL: assetURL,
		Base:     base,
	}, login.GetUser(r.Context())).Render(r.Context(), w)
}
