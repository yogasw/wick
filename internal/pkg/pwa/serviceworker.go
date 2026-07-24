package pwa

import (
	_ "embed"
	"net/http"
)

// serviceWorkerJS is the PWA service worker, served from the site root so
// its registration scope covers the whole app ("/"). A service worker with
// a fetch handler is one of Chrome's hard requirements for the install
// prompt to appear.
//
//go:embed sw.js
var serviceWorkerJS []byte

// ServiceWorkerHandler serves /sw.js. It must be registered at the root
// path (not under /public/) so the default scope is "/". no-cache lets the
// browser pick up a new worker on each deploy.
func ServiceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write(serviceWorkerJS)
}

// DisabledServiceWorkerHandler serves /sw.js as a 404 (WICK_SW_ENABLED=false).
// The service worker's own caching (stale-while-revalidate on static
// assets, cache-fallback on navigations) can pin a stale build across a
// dev rebuild, so this lets a dev server run with no SW at all. A 404
// here — rather than just not registering — makes the browser drop any
// worker it already registered for this origin on the next page load,
// so switching the flag off actually clears prior state instead of
// leaving an old worker running alongside the new "disabled" server.
func DisabledServiceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
