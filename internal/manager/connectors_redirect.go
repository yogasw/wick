package manager

import (
	"net/http"
	"net/url"
	"strings"
)

// agentsConnectorsBase is the Agents-hosted connectors page. Connectors are
// surfaced as an Agents-domain feature, so every /manager/connectors* PAGE
// load is bounced here. The SPA + all /manager/api/connectors* JSON routes
// stay put — only the human-facing shell moves into the Agents chrome.
const agentsConnectorsBase = "/tools/agents/connectors"

// serveConnectorsShell replaces serveSPAShell for connectors PAGE routes.
// Instead of rendering the manager chrome, it 302s to the Agents-hosted
// connectors page, carrying the client-route path (everything after
// /manager) in ?deep= so a reload or deep link reopens the same view
// inside the Agents shell. Jobs/tools/audit pages keep serveSPAShell and
// stay under /manager untouched.
func (h *Handler) serveConnectorsShell(w http.ResponseWriter, r *http.Request) {
	// Client route = path with the /manager mount base stripped (matches
	// router.ts routeFromPath: /manager/connectors/x → /connectors/x).
	deep := strings.TrimPrefix(r.URL.Path, spaBase)
	target := agentsConnectorsBase
	if deep != "" && deep != "/" {
		target += "?deep=" + url.QueryEscape(deep)
	}
	http.Redirect(w, r, target, http.StatusFound)
}
