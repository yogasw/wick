package manager

import (
	"embed"

	"github.com/yogasw/wick/internal/pkg/spa"
)

// spaEmbedded carries the Vite-built manager SPA tree. The build pipeline
// (`npm --workspace=@wick-fe/manager run build` from `fe/`) writes assets
// into `dist/manager/`; this file is the only Go-side glue.
//
// dist/.gitkeep is committed so this embed always has a directory to read
// even on a fresh checkout where the bundle has not been built yet.
//
//go:embed all:dist
var spaEmbedded embed.FS

// spaLoader handles FS selection (embed vs live-disk) and asset URL resolution.
// Auto-switches to os.DirFS and registers for global dev-reload watching when
// WICK_DEV_REPO_ROOT is set.
var spaLoader = spa.New(spaEmbedded, "internal/manager")

// spaAssetBase is the URL prefix the Vite bundle is served under. The Vite
// build bakes this as the asset `base`, so the hashed bundle + chunk URLs in
// dist/manager/index.html resolve back to spaAssetHandler.
const spaAssetBase = "/manager/_app/"

// spaBase is injected into the #app div's data-base attribute as the
// client-side route prefix (e.g. /manager/connectors → client route /connectors).
const spaBase = "/manager"

// spaAssetURL returns the hashed entry .js bundle URL for the manager app.
func spaAssetURL() string { return spaLoader.AssetURL("manager", spaAssetBase+"assets") }
