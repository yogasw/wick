package manager

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager/view"
)

// registerSPAAssets wires the manager SPA's Vite asset handler. The bundle
// is served from the all:dist embed at spaAssetBase (the value baked as the
// Vite `base`), so the hashed script + chunk URLs in dist/manager/index.html
// resolve back here. Behind the same auth as the page routes. The "/" suffix
// makes it a subtree match for /manager/_app/assets/*.
func (h *Handler) registerSPAAssets(mux *http.ServeMux, authMidd *login.Middleware) {
	mux.Handle("GET "+spaAssetBase, authMidd.RequireAuth(http.HandlerFunc(h.spaAssetHandler)))
}

// spaAssetHandler serves the hashed Vite bundle files from the embed under
// spaAssetBase with an immutable cache. Only assets/* live here — page
// routes render the thin-shell via serveSPAShell instead.
func (h *Handler) spaAssetHandler(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(spaFS, "dist/manager")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, spaAssetBase)
	if !strings.HasPrefix(rest, "assets/") {
		http.NotFound(w, r)
		return
	}
	assetPath := strings.TrimPrefix(rest, "assets/")
	data, err := fs.ReadFile(sub, "assets/"+assetPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(assetPath))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

// serveSPAShell renders the manager SPA thin-shell inside the host
// ui.Layout chrome. Every manager *page* GET route (/manager,
// /manager/connectors/..., /manager/jobs/{key}, etc.) resolves here; the
// SPA's client router then resolves the rest of the path. The theme + app
// name flow from the request context (set by the auth middleware) into
// ui.Layout, so the SPA inherits the host theme with no manual injection.
func (h *Handler) serveSPAShell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	vm := view.ManagerSPAVM{
		Base:     spaBase,
		AssetURL: spaAssetURL(),
		User:     login.GetUser(r.Context()),
	}
	_ = view.ManagerSPA(vm).Render(r.Context(), w)
}

// contentTypeFor maps an asset extension to its MIME type for the
// embed-served bundle files.
func contentTypeFor(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".map", ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".ico":
		return "image/x-icon"
	}
	return "application/octet-stream"
}
