package manager

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// spaFS is the filesystem the SPA handler reads from. Defaults to the
// compile-time embed (production). When WICK_DEV_REPO_ROOT is set,
// swapped to an os.DirFS so Vite watch rebuilds are picked up without
// recompiling Go — same convention as internal/tools/*/spa.go.
var spaFS fs.FS = spaEmbedded

// spaLiveDisk is true when spaFS points at a real filesystem. In that
// mode the asset-URL resolver re-reads index.html on every request so
// Vite's new-hash-on-every-rebuild flow surfaces immediately.
var spaLiveDisk bool

func init() {
	root := os.Getenv("WICK_DEV_REPO_ROOT")
	if root == "" {
		return
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	distDir := filepath.Join(abs, "internal", "manager", "dist")
	if info, err := os.Stat(distDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "[spa] manager has no dist/ at %s — falling back to embed\n", distDir)
		return
	}
	managerDir := filepath.Join(abs, "internal", "manager")
	fmt.Fprintf(os.Stderr, "[spa] live disk mode — manager reading from %s\n", managerDir)
	spaFS = os.DirFS(managerDir)
	spaLiveDisk = true
}
