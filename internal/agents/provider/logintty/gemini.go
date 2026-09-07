package logintty

// gemini per-type login TTY support. Only the account probe (connect
// status display) is implemented today; the TTY login command lands
// here when gemini reconnect is enabled.

import (
	"os"
	"path/filepath"
	"time"
)

// geminiConfigDir is always ~/.gemini — the gemini CLI has no config
// dir env override for its credential store.
func geminiConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini")
}

func readGeminiAccount(dir string) Account {
	var acc Account
	var accounts struct {
		Active string `json:"active"`
	}
	if readJSON(filepath.Join(dir, "google_accounts.json"), &accounts) {
		acc.Email = accounts.Active
	}
	var creds struct {
		ExpiryDate int64 `json:"expiry_date"`
	}
	if readJSON(filepath.Join(dir, "oauth_creds.json"), &creds) {
		acc.Connected = true
		acc.AuthMethod = "Google"
		if creds.ExpiryDate > 0 {
			acc.ExpiresAt = time.UnixMilli(creds.ExpiryDate)
		}
	}
	return acc
}
