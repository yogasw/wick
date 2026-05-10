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

	var candidates []string
	switch t {
	case TypeClaude:
		candidates = []string{
			filepath.Join(home, ".local", "bin", "claude.exe"),
			filepath.Join(home, ".local", "bin", "claude.cmd"),
			filepath.Join(appData, "npm", "claude.cmd"),
			filepath.Join(appData, "npm", "claude.exe"),
			filepath.Join(localAppData, "Programs", "claude", "claude.exe"),
			filepath.Join(programFiles, "Claude", "claude.exe"),
		}
	case TypeCodex:
		candidates = []string{
			filepath.Join(appData, "npm", "codex.cmd"),
			filepath.Join(appData, "npm", "codex.exe"),
			filepath.Join(home, ".local", "bin", "codex.exe"),
		}
	case TypeGemini:
		candidates = []string{
			filepath.Join(appData, "npm", "gemini.cmd"),
			filepath.Join(appData, "npm", "gemini.exe"),
			filepath.Join(home, ".local", "bin", "gemini.exe"),
		}
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
