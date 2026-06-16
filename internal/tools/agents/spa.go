package agents

import (
	"embed"
	"io/fs"

	"github.com/yogasw/wick/internal/pkg/spa"
)

// spaEmbedded carries the Vite-built Svelte SPA tree. The build pipeline
// (`npm run build` from `fe/`) writes assets into `dist/<app>/`; this file is
// the only Go-side glue. Served by spa_handler.go at /tools/workflow/.
//
//go:embed all:dist
var spaEmbedded embed.FS

// spaLoader handles FS selection (embed vs live-disk) and per-app asset URL
// resolution. Auto-switches to os.DirFS and registers for global dev-reload
// watching when WICK_DEV_REPO_ROOT is set.
var spaLoader = spa.New(spaEmbedded, "internal/tools/agents")

// SPAFS is kept for backward compatibility with spa_handler.go (reads the FS
// directly to serve assets + the shell). Points at the same FS as spaLoader.
var SPAFS fs.FS = func() fs.FS { return spaLoader.FS() }()

// spaAssetURL returns the hashed entry .js bundle URL for app, read from
// dist/<app>/index.html. The fallback base mirrors the Vite-baked asset path
// (/tools/agents/workflow/<app>/assets) for the rare hand-rolled-dist case.
func spaAssetURL(app string) string {
	return spaLoader.AssetURL(app, "/tools/agents"+spaPrefix+app+"/assets")
}
