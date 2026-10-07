package admin

import (
	"embed"

	"github.com/yogasw/wick/internal/pkg/spa"
)

// spaEmbedded carries the Vite-built admin SPA tree (currently the user
// analytics module). The build pipeline — `npm --workspace=@wick-fe/admin-
// analytics run build` from `fe/` — writes into `dist/analytics/`; this file
// is the only Go-side glue.
//
// dist/.gitkeep is committed so the embed always has a directory to read on a
// fresh checkout where the bundle has not been built yet.
//
//go:embed all:dist
var spaEmbedded embed.FS

// spaLoader picks embed vs live disk and resolves the hashed entry bundle.
var spaLoader = spa.New(spaEmbedded, "internal/admin")

// spaAssetBase is where the bundle is served from, and what Vite bakes in as
// the asset base so the hashed URLs in index.html resolve back to us.
const spaAssetBase = "/admin/_app/"

// spaAssetURL returns the hashed entry .js for the analytics module.
func spaAssetURL() string { return spaLoader.AssetURL("analytics", spaAssetBase+"assets") }
