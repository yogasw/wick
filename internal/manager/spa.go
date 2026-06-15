package manager

import (
	"embed"
	"io/fs"
)

// spaEmbedded carries the Vite-built manager SPA tree. The build pipeline
// (`npm --workspace=@wick-fe/manager run build` from `fe/`) writes assets
// into `dist/manager/`; this file is the only Go-side glue. Served by
// spa_handler.go at /modules/manager/app/.
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
