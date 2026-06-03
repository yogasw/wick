package agents

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/yogasw/wick/pkg/tool"
)

// SPA mount point under the agents tool — kept distinct from the legacy
// templ routes so both can run side-by-side during the migration phase
// (see internal/docs/workflow/svelte-migration.md). The dist tree shape
// is `dist/<app>/...` with one Vite app per directory (currently only
// `workflow/`); the SPA shell handler rewrites unknown paths to the
// matching app's index.html so client-side routing works without server
// rewrites.
const spaPrefix = "/workflow/"

// registerSPA wires the SPA shell + asset handler onto the agents
// router. We use HandleRaw with one internal mux so we own the
// specificity rules ourselves (Go 1.22's ServeMux panics when a
// method-less `Static` subtree overlaps a `GET {path...}` wildcard,
// which is exactly what serving /assets/* + the shell catch-all would
// look like otherwise).
func registerSPA(r tool.Router) {
	// The real toolRouter (internal/pkg/api/toolrouter.go) mounts a
	// HandleRaw handler WITHOUT stripping the prefix, so the handler
	// sees the full URL path. We strip here so spaHandler can work in
	// SPA-root-relative terms (e.g. "workflow/edit/abc" instead of
	// "/tools/agents/workflow/workflow/edit/abc").
	mount := strings.TrimSuffix(r.Meta().Path+spaPrefix, "/")
	r.HandleRaw(spaPrefix, func(_ tool.ConfigReader) http.Handler {
		return http.StripPrefix(mount, http.HandlerFunc(spaHandler))
	})
}

// spaHandler serves /assets/* from the embed and any other path with
// the matching app's index.html. The Vite config bakes the SPA's base
// URL as `/tools/workflow/<app>/` so asset references in index.html
// land back on this handler.
func spaHandler(w http.ResponseWriter, r *http.Request) {
	// Request path arrives stripped of the agents mount prefix already
	// (tool.HandleRaw uses http.StripPrefix). It's relative to the SPA
	// root — e.g. "workflow/assets/index-xxx.js" or "workflow/edit/abc".
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		http.Redirect(w, r, "workflow/", http.StatusFound)
		return
	}

	app, rest := splitFirstSegment(p)
	sub, err := fs.Sub(SPAFS, "dist/"+app)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := sub.Open("index.html"); err != nil {
		// App folder missing or shell never built. 404 surfaces the
		// missing-build state without confusing the network panel.
		http.Error(w, "SPA shell not built yet — run `npm run build:workflow` in fe/", http.StatusNotFound)
		return
	}

	// Asset routes: `<app>/assets/...` → serve from the embed.
	if strings.HasPrefix(rest, "assets/") || rest == "assets" {
		assetPath := strings.TrimPrefix(rest, "assets/")
		f, err := sub.Open("assets/" + assetPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = f.Close()
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

	// Everything else: hand back index.html so the Svelte router resolves
	// the client-side route. Non-asset paths that happen to look like
	// files (e.g. `favicon.ico`) get the shell too — that's fine because
	// browsers ignore mismatched content types for missing favicons.
	idx, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "spa shell missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(idx)
}

func splitFirstSegment(p string) (head, rest string) {
	i := strings.IndexByte(p, '/')
	if i < 0 {
		return p, ""
	}
	return p[:i], p[i+1:]
}

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

// hasAssetExt is retained for the legacy spa_handler_test.go probe.
func hasAssetExt(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".js", ".css", ".map", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".json":
		return true
	}
	return false
}
