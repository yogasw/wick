// Package spadev exposes one helper, LiveDiskFS, that every Go tool
// shipping a Vite-built SPA wires into its `init()` so the dev loop
// (`npm run build:watch`) can serve fresh bundles without recompiling
// the wick binary.
//
// Convention: every tool keeps its SPA tree at
// `internal/tools/<tool>/dist/<app>/`. When the env var
// WICK_DEV_REPO_ROOT points at the wick checkout, each tool's `spa.go`
// asks for `LiveDiskFS("<tool>")` and swaps its `SPAFS embed.FS` for
// an `os.DirFS` rooted at the tool dir. Production binaries leave the
// var unset and stay on the compile-time embed.
package spadev

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LiveDiskFS returns a live filesystem view of an SPA dist tree for the
// named Go tool, plus a flag telling the caller whether it should
// disable any per-process URL caches (live mode → cache off because
// Vite rebuilds change the hashed bundle name).
//
// Returns (nil, false) when the env var is unset, points at a missing
// path, or the tool has no dist subdir yet. Callers fall back to their
// compile-time embed in that case.
func LiveDiskFS(toolName string) (fs.FS, bool) {
	root := os.Getenv("WICK_DEV_REPO_ROOT")
	if root == "" {
		fmt.Fprintf(os.Stderr, "[spa] %s: WICK_DEV_REPO_ROOT unset — using embed\n", toolName)
		return nil, false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[spa] WICK_DEV_REPO_ROOT resolve failed: %v — falling back to embed\n", err)
		return nil, false
	}
	toolDir := filepath.Join(abs, "internal", "tools", toolName)
	distDir := filepath.Join(toolDir, "dist")
	if info, err := os.Stat(distDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "[spa] %s has no dist/ at %s — falling back to embed\n", toolName, distDir)
		return nil, false
	}
	fmt.Fprintf(os.Stderr, "[spa] live disk mode — %s reading from %s\n", toolName, toolDir)
	return os.DirFS(toolDir), true
}
