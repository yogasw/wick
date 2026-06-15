package manager

import (
	"embed"
	"io/fs"
)

// spaEmbedded carries the Vite-built manager SPA tree. The build pipeline
// (`npm --workspace=@wick-fe/manager run build` from `fe/`) writes assets
// into `dist/manager/`; this file is the only Go-side glue. The bundle
// assets are served by spa_handler.go at spaAssetBase (/manager/_app/),
// while the page routes render the thin-shell via serveSPAShell.
//
// dist/.gitkeep is committed so this embed always has a directory to read
// even on a fresh checkout where the bundle has not been built yet.
//
//go:embed all:dist
var spaEmbedded embed.FS

// spaFS is the filesystem the SPA handler reads from. Kept as a var so a
// future dev loop could swap it for an os.DirFS; production uses the
// compile-time embed.
var spaFS fs.FS = spaEmbedded
