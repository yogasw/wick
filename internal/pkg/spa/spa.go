// Package spa provides a unified SPA (Single-Page Application) loader for
// wick's Vite-built Svelte frontends. It handles:
//
//  1. FS selection — compile-time embed.FS in production; os.DirFS when
//     WICK_DEV_REPO_ROOT is set so Vite watch rebuilds are served without
//     recompiling Go.
//
//  2. Asset URL resolution — reads the hashed bundle src from each app's
//     index.html (multi-app: one dist/ root holds dist/<app>/index.html).
//     Cached per-app in production; re-read on every call in live-disk mode.
//
//  3. Dev reload — a single global SSE endpoint (/_dev/reload) that watches
//     all registered dist/ directories and broadcasts a "reload" event to every
//     connected browser tab whenever any bundle rebuilds. Only active when
//     WICK_DEV_REPO_ROOT is set. Call RegisterGlobalHandler(mux) once in the
//     root server; ui.Layout injects DevReloadScript() unconditionally
//     (returns "" in production).
package spa

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// bundleSrcRe matches the Vite-injected module script src in index.html. Using
// the src Vite wrote in the same flush dodges the "stale hashed bundle lingers
// on disk" pitfall a plain readdir would hit (vite build --watch keeps
// emptyOutDir off, so old bundles accumulate).
var bundleSrcRe = regexp.MustCompile(`<script[^>]*\bsrc="([^"]*/index-[^"]+\.js)"`)

const devReloadPath = "/_dev/reload"

// global registry of live-disk watch dirs, populated by New().
var (
	globalMu        sync.Mutex
	globalWatchDirs []string
)

// DevReloadScript returns the inline <script> tag that connects to
// /_dev/reload and auto-reloads the page on each build. Returns "" when
// WICK_DEV_REPO_ROOT is not set (production). Safe to inject unconditionally.
func DevReloadScript() string {
	globalMu.Lock()
	active := len(globalWatchDirs) > 0
	globalMu.Unlock()
	if !active {
		return ""
	}
	// Skip reload only when a modal is actually VISIBLE — not merely present
	// in the DOM. ui.Dialog() always renders a hidden <div role="dialog">, so
	// a plain presence check would never reload. offsetParent === null means
	// the element (or an ancestor) is display:none / hidden.
	return `<script>(function(){` +
		`var es=new EventSource('` + devReloadPath + `');` +
		`es.addEventListener('reload',function(){` +
		`var open=false;` +
		`document.querySelectorAll('dialog[open],[role="dialog"]').forEach(function(el){` +
		`if(el.offsetParent!==null)open=true;` +
		`});` +
		`if(!open)location.reload();` +
		`});` +
		`es.onerror=function(){es.close();};` +
		`})()</script>`
}

// RegisterGlobalHandler registers GET /_dev/reload on mux only when
// WICK_DEV_REPO_ROOT is set. No-op otherwise — call unconditionally in server.
func RegisterGlobalHandler(mux *http.ServeMux) {
	globalMu.Lock()
	dirs := append([]string(nil), globalWatchDirs...)
	globalMu.Unlock()
	if len(dirs) == 0 {
		return
	}
	mux.Handle("GET "+devReloadPath, globalSSEHandler(dirs))
}

func globalSSEHandler(watchDirs []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use ResponseController instead of a direct http.Flusher assertion:
		// the server wraps the ResponseWriter in middleware, and the controller
		// traverses the Unwrap() chain to reach the real flusher.
		rc := http.NewResponseController(w)
		flush := func() { _ = rc.Flush() }

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			http.Error(w, "watcher failed", http.StatusInternalServerError)
			return
		}
		defer watcher.Close()
		for _, dir := range watchDirs {
			// Watch the dist/ root and every immediate subdir so a rebuild of
			// any app (dist/<app>/index.html) is detected. Vite writes the
			// index.html inside the per-app subdir.
			_ = watcher.Add(dir)
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						_ = watcher.Add(filepath.Join(dir, e.Name()))
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, ": connected\n\n")
		flush()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) &&
					filepath.Base(event.Name) == "index.html" {
					fmt.Fprintf(w, "event: reload\ndata: {}\n\n")
					flush()
				}
			case <-watcher.Errors:
				return
			case <-ticker.C:
				fmt.Fprintf(w, ": keepalive\n\n")
				flush()
			}
		}
	})
}

// Loader is the live-disk-aware SPA filesystem + asset-URL resolver for one
// dist/ root. A root holds one or more apps at dist/<app>/. Create with New.
type Loader struct {
	fs       fs.FS
	liveDisk bool

	mu    sync.Mutex
	cache map[string]string // app → resolved bundle URL (production only)
}

// New creates a Loader for a single dist/ root and auto-registers it in the
// global dev-reload registry when WICK_DEV_REPO_ROOT is set.
//
//   - embedded: compile-time embed.FS (must contain dist/...)
//   - repoRelDir: repo-root-relative path to the dir holding dist/,
//     e.g. "internal/manager" or "internal/tools/agents"
//
// When WICK_DEV_REPO_ROOT is set and dist/ exists on disk, the loader switches
// to os.DirFS so Vite watch rebuilds surface without a Go recompile.
func New(embedded fs.FS, repoRelDir string) *Loader {
	l := &Loader{fs: embedded, cache: map[string]string{}}

	root := os.Getenv("WICK_DEV_REPO_ROOT")
	if root == "" {
		return l
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return l
	}
	dir := filepath.Join(abs, filepath.FromSlash(repoRelDir))
	distDir := filepath.Join(dir, "dist")
	if info, err := os.Stat(distDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "[spa] %s: no dist/ at %s — using embed\n", repoRelDir, distDir)
		return l
	}
	fmt.Fprintf(os.Stderr, "[spa] live disk — %s → %s\n", repoRelDir, dir)
	l.fs = os.DirFS(dir)
	l.liveDisk = true

	globalMu.Lock()
	globalWatchDirs = append(globalWatchDirs, distDir)
	globalMu.Unlock()
	return l
}

// FS returns the active filesystem (embed or live-disk), rooted so that
// dist/<app>/... paths resolve.
func (l *Loader) FS() fs.FS { return l.fs }

// IsLiveDisk reports whether live-disk mode is active.
func (l *Loader) IsLiveDisk() bool { return l.liveDisk }

// AssetURL returns the absolute URL of the hashed entry .js bundle for app,
// read from dist/<app>/index.html. fallbackBase is the URL prefix used to build
// the asset URL when index.html has no script tag (rare: a hand-rolled dist
// tree) — e.g. "/manager/_app/assets". Returns "" when not built yet.
//
// Cached per-app in production; re-read on every call in live-disk mode so a
// Vite rebuild's new hash surfaces on the next render.
func (l *Loader) AssetURL(app, fallbackBase string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.liveDisk {
		if v, ok := l.cache[app]; ok {
			return v
		}
	}

	// Authoritative: the src Vite wrote into index.html.
	if data, err := fs.ReadFile(l.fs, "dist/"+app+"/index.html"); err == nil {
		if m := bundleSrcRe.FindSubmatch(data); m != nil {
			return l.store(app, string(m[1]))
		}
	}

	// Fallback: scan assets/ for index-*.js (lexicographically first).
	entries, err := fs.ReadDir(l.fs, "dist/"+app+"/assets")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "index-") && strings.HasSuffix(n, ".js") {
			return l.store(app, strings.TrimRight(fallbackBase, "/")+"/"+n)
		}
	}
	return ""
}

func (l *Loader) store(app, url string) string {
	if !l.liveDisk {
		l.cache[app] = url
	}
	return url
}
