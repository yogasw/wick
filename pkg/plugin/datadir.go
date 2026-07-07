package plugin

import (
	"os"
	"path/filepath"
)

// DataDir returns the persistent per-plugin work directory for the given plugin
// key — the place a plugin should store downloaded assets, caches, and session
// state so they SURVIVE (unlike os.TempDir(), which the OS / Storage Sense
// wipes).
//
// It is resolved entirely plugin-side from the running binary's own location —
// no env var, no host cooperation. Installed plugin binaries live at
//
//	<appDataDir>/plugins/connectors/<key>/<key>[.exe]
//
// so the sibling data dir is two levels up from the binary's folder, alongside
// the connectors/ dir:
//
//	<appDataDir>/plugins/<key>/
//
// This sits ALONGSIDE the connectors/ dir (binaries + plugin.json), never
// inside it, so wiping a plugin's data never touches its installed binary or
// catalog.
//
// If the binary isn't laid out under a .../plugins/connectors/ tree (e.g. a bare
// `go run` during development, or a `go test`), it falls back to
// <os-temp>/wick-plugins/<key> — good enough for throwaway runs, and the only
// case where OS temp is acceptable.
func DataDir(key string) string {
	exe, err := os.Executable()
	if err == nil {
		if resolved, ok := dataDirFromExe(exe, key); ok {
			return resolved
		}
	}
	return filepath.Join(os.TempDir(), "wick-plugins", key)
}

// dataDirFromExe derives <appDataDir>/plugins/<key> from the binary path when it
// sits under a .../plugins/connectors/<key>/ dir (the install layout the host
// unzips into). Split out so it's unit-testable without touching the real
// executable path. Returns ok=false when the layout isn't recognized, so
// DataDir can fall back to temp.
func dataDirFromExe(exe, key string) (string, bool) {
	binDir := filepath.Dir(exe)         // .../plugins/connectors/<key>
	connectorsDir := filepath.Dir(binDir) // .../plugins/connectors
	if filepath.Base(connectorsDir) != "connectors" {
		return "", false
	}
	pluginsDir := filepath.Dir(connectorsDir) // .../plugins
	return filepath.Join(pluginsDir, key), true
}
