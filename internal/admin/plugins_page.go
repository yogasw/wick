package admin

import (
	"net/http"

	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
)

// pluginsPage hosts the manager SPA's plugins view inside the admin chrome.
// Same pattern as the Agents connectors page: the bundle, its asset handler
// and its /manager/api/plugins calls all stay where they are — only the
// outer shell is admin's, so the nav and the admin gate match every other
// /admin page.
func (h *Handler) pluginsPage(w http.ResponseWriter, r *http.Request) {
	assetURL, base := manager.SPAMount()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = view.PluginsPage(view.PluginsSPAVM{
		AssetURL: assetURL,
		Base:     base,
	}, login.GetUser(r.Context())).Render(r.Context(), w)
}
