package manager

import (
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
)

// spaMount is the URL prefix the manager SPA is served under. It lives
// alongside the legacy server-rendered templ pages at /modules/manager/
// (static) and /manager/* (handlers) so both run side by side during the
// migration. The Vite build bakes this as the asset `base`, so script and
// chunk URLs in index.html resolve back to this handler.
const spaMount = "/modules/manager/app"

// spaBase is the value injected into the #app div's data-base attribute.
// The SPA router reads it as the client-side route prefix; the API client
// uses server-absolute /manager paths and ignores it.
const spaBase = spaMount

// dataBaseRe matches the data-base attribute on the SPA mount div so the
// handler can normalize it to the live mount path regardless of what the
// build baked in.
var dataBaseRe = regexp.MustCompile(`data-base="[^"]*"`)

// htmlClassRe matches the class attribute on the root <html> element of
// the served SPA shell so the handler can inject the user's resolved
// theme classes — the SPA is served outside ui.Layout, which would
// otherwise set them, so without this it always renders light.
var htmlClassRe = regexp.MustCompile(`(<html[^>]*\sclass=")[^"]*(")`)

// headOpenRe matches the opening <head> tag so the handler can inject the
// system-preference theme script when the user has no stored theme,
// mirroring the no-preference branch of ui.Layout.
var headOpenRe = regexp.MustCompile(`(?i)<head>`)

// applyTheme rewrites the SPA shell's <html class> with the theme
// resolved from ctx so the standalone SPA reflects the same theme as the
// server-rendered pages. When no theme is stored it leaves the class
// empty and injects the device color-scheme script into <head> before
// first paint, exactly as ui.Layout does.
func applyTheme(idx []byte, themeClass string) []byte {
	idx = htmlClassRe.ReplaceAll(idx, []byte(`${1}`+themeClass+`${2}`))
	if themeClass == "" {
		script := []byte("<head><script>" + ui.SystemThemeScript + "</script>")
		idx = headOpenRe.ReplaceAll(idx, script)
	}
	return idx
}

// registerSPA wires the manager SPA shell + asset handler. Mounted behind
// the same auth as the templ pages. The "/" suffix on the pattern makes
// it a subtree match so client-side routes (e.g. /modules/manager/app/foo)
// all resolve to the SPA shell.
func (h *Handler) registerSPA(mux *http.ServeMux, authMidd *login.Middleware) {
	mux.Handle("GET "+spaMount+"/", authMidd.RequireAuth(http.HandlerFunc(h.spaHandler)))
}

// spaHandler serves /modules/manager/app/assets/* from the embed with an
// immutable cache, and every other subpath with index.html (data-base
// normalized) so the Svelte router resolves the client-side route.
func (h *Handler) spaHandler(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(spaFS, "dist/manager")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, spaMount+"/")

	if strings.HasPrefix(rest, "assets/") {
		assetPath := strings.TrimPrefix(rest, "assets/")
		data, err := fs.ReadFile(sub, "assets/"+assetPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentTypeFor(assetPath))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
		return
	}

	idx, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "manager SPA not built yet — run `npm --workspace=@wick-fe/manager run build` in fe/", http.StatusNotFound)
		return
	}
	idx = dataBaseRe.ReplaceAll(idx, []byte(`data-base="`+spaBase+`"`))
	idx = applyTheme(idx, ui.HTMLThemeClass(r.Context()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(idx)
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
