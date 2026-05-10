//go:build !windows

package provider

import (
	"os"
	"path/filepath"
)

// scanKnownLocations probes well-known install paths for each provider
// type when PATH lookup fails. macOS/Linux: claude installer drops to
// ~/.local/bin or ~/.claude/local/; npm globals land in any of half a
// dozen places depending on the Node version manager (vanilla npm,
// nvm, asdf, fnm, volta) plus pnpm/yarn globals plus system package
// managers (homebrew, MacPorts).
//
// Tray-launched wick inherits a minimal PATH, so we probe these
// directly by file existence rather than relying on PATH.
func scanKnownLocations(t Type) (string, bool) {
	home, _ := os.UserHomeDir()
	name := string(t)

	var candidates []string

	// Per-user bin dirs (npm prefix overrides, claude installer).
	candidates = append(candidates,
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
		filepath.Join(home, ".npm-global", "bin", name),
		filepath.Join(home, ".local", "share", "pnpm", name),
		filepath.Join(home, ".yarn", "bin", name),
		filepath.Join(home, ".volta", "bin", name),
		filepath.Join(home, ".asdf", "shims", name),
		filepath.Join(home, ".bun", "bin", name),
	)

	// nvm + fnm install Node into versioned dirs — glob all versions
	// and check each one's bin/. Sorted by Glob alphabetically; first
	// hit wins, so the latest installed Node usually wins for users
	// who upgrade in place.
	for _, pat := range []string{
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin", name),
		filepath.Join(home, ".local", "share", "fnm", "node-versions", "*", "installation", "bin", name),
		filepath.Join(home, "Library", "Application Support", "fnm", "node-versions", "*", "installation", "bin", name),
	} {
		if matches, _ := filepath.Glob(pat); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}

	// System bins (homebrew on Apple Silicon + Intel, MacPorts, distro).
	candidates = append(candidates,
		"/opt/homebrew/bin/"+name,
		"/usr/local/bin/"+name,
		"/opt/local/bin/"+name,
		"/usr/bin/"+name,
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}
