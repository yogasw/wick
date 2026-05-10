//go:build windows

package provider

import (
	"os"
	"path/filepath"
)

// scanKnownLocations probes well-known install paths for each provider
// type when PATH lookup fails. Order matters: first hit wins, so the
// most common installer location goes first.
//
// Why hardcoded list: claude/codex/gemini installers (curl|sh, npm,
// official MSI) all drop into predictable folders that tray-launched
// processes don't see in inherited %PATH%. Scanning them eliminates
// the "edit binary path manually" step for the common case.
func scanKnownLocations(t Type) (string, bool) {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")
	name := string(t)

	// npmRoots collects every plausible "npm global bin" directory:
	// the standard %APPDATA%\npm location and common Node-version-
	// manager install roots (nvm4w, nvm-windows, fnm, volta) which
	// drop npm globals next to node.exe instead of %APPDATA%\npm.
	// Tray-launched wick inherits a minimal PATH that usually
	// excludes these, so we probe them by file existence.
	npmRoots := []string{
		filepath.Join(appData, "npm"),
		`C:\nvm4w\nodejs`,
		filepath.Join(home, "AppData", "Roaming", "nvm"),
		filepath.Join(localAppData, "nvm"),
		filepath.Join(localAppData, "fnm_multishells"),
		filepath.Join(localAppData, "Volta", "bin"),
		filepath.Join(programFiles, "nodejs"),
	}

	var candidates []string
	for _, root := range npmRoots {
		candidates = append(candidates,
			filepath.Join(root, name+".cmd"),
			filepath.Join(root, name+".exe"),
		)
	}

	switch t {
	case TypeClaude:
		// Official claude installer drops outside npm too.
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "claude.exe"),
			filepath.Join(home, ".local", "bin", "claude.cmd"),
			filepath.Join(localAppData, "Programs", "claude", "claude.exe"),
			filepath.Join(programFiles, "Claude", "claude.exe"),
		)
	case TypeCodex, TypeGemini:
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", name+".exe"),
		)
	}

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}
