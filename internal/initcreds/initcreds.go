// Package initcreds writes / clears the initial-admin credentials file.
//
// On first boot wick may auto-generate the admin password (when env
// APP_ADMIN_PASSWORD is empty). The plaintext is dropped into a single
// file under the per-app data dir so the operator can recover it after
// installing — the dialog is then "log in, change the password, this
// file deletes itself". After admin_password_changed is set, callers
// invoke Clear to remove the file.
package initcreds

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/userconfig"
)

const fileName = "INITIAL_CREDENTIALS.txt"

// Path returns the absolute file path under ~/.<appName>/.
// appName empty → falls back to the binary basename via userconfig.Dir.
func Path(appName string) (string, error) {
	dir, err := userconfig.Dir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Write creates the credentials file with mode 0600 (owner read/write).
// Overwrites any existing file. Caller passes appName so the file lands
// in the same per-app dir as logs / config.
func Write(appName, email, password, appURL string) (string, error) {
	path, err := Path(appName)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	body := fmt.Sprintf(
		"%s — initial admin credentials\n"+
			"-------------------------------------------------\n"+
			"URL:              %s\n"+
			"Email:            %s\n"+
			"Default password: %s\n\n"+
			"Log in and change the default password from /profile/setup.\n"+
			"This file is deleted automatically once the default password is changed.\n",
		appName, appURL, email, password,
	)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Info is the parsed contents of INITIAL_CREDENTIALS.txt. Empty fields
// when parsing failed or the file is missing.
type Info struct {
	URL      string
	Email    string
	Password string
}

// Read parses the credentials file. Returns ok=false (and a zero Info)
// when the file is missing — callers treat that as "already changed".
// Surface-level parser only: looks for the "URL:", "Email:",
// "Default password:" prefixes Write emits.
func Read(appName string) (Info, bool) {
	path, err := Path(appName)
	if err != nil {
		return Info{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Info{}, false
	}
	var out Info
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "URL:"):
			out.URL = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		case strings.HasPrefix(line, "Email:"):
			out.Email = strings.TrimSpace(strings.TrimPrefix(line, "Email:"))
		case strings.HasPrefix(line, "Default password:"):
			out.Password = strings.TrimSpace(strings.TrimPrefix(line, "Default password:"))
		}
	}
	return out, out.Email != "" && out.Password != ""
}

// Clear removes the credentials file. Missing file is not an error.
func Clear(appName string) error {
	path, err := Path(appName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
