package admin

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaAssetHandler serves the analytics module's hashed Vite files from the
// embed under spaAssetBase, with an immutable cache. Only assets/* live
// here — the page route renders the shell that pulls them in.
//
// Mirrors internal/manager/spa_handler.go rather than sharing it: the two
// embeds are different trees, and one generic helper would need the FS, the
// sub-path and the base passed in anyway.
func (h *Handler) spaAssetHandler(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(spaLoader.FS(), "dist/analytics")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, spaAssetBase)
	if !strings.HasPrefix(rest, "assets/") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(sub, rest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", adminAssetContentType(rest))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

func adminAssetContentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".map", ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".woff2":
		return "font/woff2"
	}
	return "application/octet-stream"
}
