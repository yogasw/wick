// Package external wraps third-party links as tool.Module entries so
// they show up on the home grid and palette alongside in-app tools.
// Each link is metadata + a redirect handler — no view, no service, no
// JS. Add a new link by appending to the list in registry.go.
package external

import (
	"net/http"

	"github.com/yogasw/wick/pkg/tool"
)

// Register installs a redirect at /tools/{meta.Key} so direct hits
// (shared links, bookmarks, the Ctrl+K palette pre-fetch) still land
// at the external URL. Visibility/tag enforcement is applied upstream
// by the /tools/* RequireToolAccess middleware.
//
// The same Register backs every external link; the redirect target is
// read per-request from c.Meta().ExternalURL.
func Register(r tool.Router) {
	r.GET("/", func(c *tool.Ctx) {
		c.Redirect(c.Meta().ExternalURL, http.StatusFound)
	})
}
