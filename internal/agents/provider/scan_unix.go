//go:build !windows

package provider

import (
	"os"
	"path/filepath"
)

// scanKnownLocations probes well-known install paths for each provider
// type when PATH lookup fails. macOS/Linux: claude installer drops to
// ~/.local/bin or ~/.claude/local/, npm globals to ~/.npm-global/bin
// or /usr/local/bin.
func scanKnownLocations(t Type) (string, bool) {
	home, _ := os.UserHomeDir()
	name := string(t)

	candidates := []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
		filepath.Join(home, ".npm-global", "bin", name),
		"/usr/local/bin/" + name,
		"/opt/homebrew/bin/" + name,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}
