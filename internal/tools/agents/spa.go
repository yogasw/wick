package agents

import (
	"embed"
	"io/fs"

	"github.com/yogasw/wick/internal/pkg/spadev"
)

// spaEmbedded carries the Vite-built Svelte SPA tree. The build pipeline
// (`npm run build` from `fe/`) writes assets into `dist/`; this file is
// the only Go-side glue. Served by spa_handler.go at /tools/agents-v2/.
//
//go:embed all:dist
var spaEmbedded embed.FS

// SPAFS is the filesystem callers should read SPA files from. Defaults
// to the compile-time embed (production); swapped to an os.DirFS at
// init when WICK_DEV_REPO_ROOT is set so the dev loop can rebuild the
// bundle (`vite build --watch`) without a Go recompile.
var SPAFS fs.FS = spaEmbedded

// spaLiveDisk is true when SPAFS points at a real filesystem. In that
// mode the asset-URL resolver skips its per-process cache so Vite's
// new-hash-on-every-rebuild flow surfaces on the next page render.
var spaLiveDisk bool

func init() {
	if live, ok := spadev.LiveDiskFS("agents"); ok {
		SPAFS = live
		spaLiveDisk = true
	}
}
