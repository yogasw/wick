//go:build headless

package systemtray

import (
	"fmt"
	"os"
)

// Run is a no-op stub for headless builds (compiled with -tags headless).
// The tray UI and its dependencies (fyne.io/systray, ICO encoder, etc.)
// are excluded; only the server / worker / daemon / MCP subcommands are
// available.
func Run(projectDir, name, appVer, wickVer, commit, builtAt, repo, pat string) {
	fmt.Fprintf(os.Stderr,
		"tray not available in headless build.\n"+
			"Use `%s start` for background daemon mode, or `%s all` to run in foreground.\n",
		name, name)
	os.Exit(1)
}
