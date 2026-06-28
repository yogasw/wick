package plugin

import (
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/userconfig"
)

// appDataDir is the per-app data directory wick keeps its DB, config, and
// plugins under (~/.<appName>, e.g. ~/.wick-agent), so plugins land in the SAME
// tree as wick.db.
//
// It resolves the app name with appname.Resolve() — the SAME source the rest of
// the app (DB path, gate, sockets) uses: ldflag → wick.yml `name:` → "wick".
// Earlier this used userconfig.Dir(""), which falls back to the BINARY's
// basename instead. That mismatched whenever the binary wasn't named exactly
// like the wick.yml app (e.g. a `wick-lab-test` debug build, or an MCP stdio
// subprocess spawned under a different name): wick.db lived in ~/.wick-lab but
// plugins were scanned in ~/.wick-lab-test, so installed plugins silently
// vanished from MCP. Resolving via appname keeps both in the same tree.
func appDataDir() string {
	name := appname.Resolve()
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, userconfig.HiddenName(name))
}

// DefaultDir is the runtime location wick scans for installed connector
// plugins: <appDataDir>/plugins/connectors, overridable with WICK_PLUGINS_DIR.
// It matches the layout `make plugins` writes to.
func DefaultDir() string {
	if d := os.Getenv("WICK_PLUGINS_DIR"); d != "" {
		return d
	}
	return filepath.Join(appDataDir(), "plugins", "connectors")
}

// RunDir is where wick pins plugin Unix sockets: <appDataDir>/run, overridable
// with WICK_PLUGIN_SOCKET_DIR. go-plugin creates the socket under here (0700)
// instead of the OS temp dir.
func RunDir() string {
	if d := os.Getenv("WICK_PLUGIN_SOCKET_DIR"); d != "" {
		return d
	}
	return filepath.Join(appDataDir(), "run")
}
