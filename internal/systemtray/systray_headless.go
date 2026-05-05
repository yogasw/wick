//go:build headless

package systemtray

import (
	"fmt"
	"os"
)

// Run is a no-op stub for headless builds (compiled with -tags headless).
// The tray UI and its dependencies (fyne.io/systray, ICO encoder, etc.)
// are excluded; only the server / worker / MCP subcommands are available.
func Run(projectDir, name, appVer, wickVer, commit, builtAt, repo, pat string) {
	fmt.Fprintln(os.Stderr, "tray not available in headless build — use `server`, `worker`, or `mcp` subcommand")
	os.Exit(1)
}
