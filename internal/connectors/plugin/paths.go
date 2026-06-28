package plugin

import (
	"os"
	"path/filepath"
)

// DefaultDir is the runtime location wick scans for installed connector
// plugins: <home>/.wick/plugins/connectors, overridable with WICK_PLUGINS_DIR.
// It matches the layout `make plugins` writes to.
func DefaultDir() string {
	if d := os.Getenv("WICK_PLUGINS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".wick", "plugins", "connectors")
}

// RunDir is where wick pins plugin Unix sockets: <home>/.wick/run, overridable
// with WICK_PLUGIN_SOCKET_DIR. go-plugin creates the socket under here (0700)
// instead of the OS temp dir.
func RunDir() string {
	if d := os.Getenv("WICK_PLUGIN_SOCKET_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".wick", "run")
}
