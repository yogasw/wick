package manager

import "net/http"

// adminPluginsPath is where the Plugins page lives now. Only the page moved;
// the /manager/api/plugins JSON routes are unchanged.
const adminPluginsPath = "/admin/plugins"

// redirectToAdminPlugins bounces the old /manager/plugins page to
// /admin/plugins, keeping the query string (tab, search, filters).
func redirectToAdminPlugins(w http.ResponseWriter, r *http.Request) {
	target := adminPluginsPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// registerPluginsRedirect wires the old page path. Kept apart from Register
// so the route itself is testable without the full manager service graph.
func registerPluginsRedirect(mux *http.ServeMux, auth func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /manager/plugins", auth(redirectToAdminPlugins))
}
