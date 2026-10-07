package api

import (
	"net/http"
	"strings"
)

// webhookBypass splits traffic under /tools/ between two handlers: a
// request whose path falls inside a declared webhook group goes to open
// (the bare tools mux, no access check), everything else goes to gated
// (the RequireToolAccess chain).
//
// The exemption exists because a webhook sender is a program. It carries
// no session cookie, so RequireToolAccess would answer it with a 302 to
// /auth/login — a redirect no webhook delivery agent follows, turning
// every delivery into a silent failure. Routing these prefixes around
// the check is what makes a tool able to receive callbacks at all.
//
// The exemption is deliberately narrow: only the exact prefixes a module
// opened via Router.WebhookGroup, matched on segment boundaries, and
// only after toolRouter.validate has rejected a group that covers a
// tool's whole mount point. Everything else about the tool — its pages,
// its other routes — stays behind the access check.
//
// prefixes is captured at boot and never mutated, so no locking is
// needed on the request path.
func webhookBypass(prefixes []string, open, gated http.Handler) http.Handler {
	if len(prefixes) == 0 {
		return gated
	}
	// Copy so a later mutation of the caller's slice cannot silently widen
	// the exemption.
	exempt := make([]string, len(prefixes))
	copy(exempt, prefixes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isWebhookPath(r.URL.Path, exempt) {
			open.ServeHTTP(w, r)
			return
		}
		gated.ServeHTTP(w, r)
	})
}

// isWebhookPath reports whether path is inside one of the exempt group
// prefixes: the prefix itself, or anything nested under it, matched on
// segment boundaries.
//
// The boundary check is the security-relevant part. A plain
// strings.HasPrefix would treat "/tools/x/webhookadmin" as inside the
// group "/tools/x/webhook" and strip its access check — a sibling route
// silently going public because its name shares a prefix. Requiring the
// next character to be "/" confines the exemption to real descendants.
func isWebhookPath(path string, exempt []string) bool {
	for _, p := range exempt {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
