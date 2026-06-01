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
