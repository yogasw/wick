//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Enable writes a freedesktop.org-spec autostart .desktop file to
// ~/.config/autostart/<appName>.desktop. Most desktop environments
// (GNOME, KDE, XFCE, etc.) honor this directory at login.
func Enable(appName string) error {
	exe := currentExe()
	if exe == "" {
		return errors.New("autostart: cannot resolve current binary path")
	}
	path, err := desktopPath(appName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: mkdir autostart: %w", err)
	}
	body := fmt.Sprintf(desktopTemplate, appName, exe, appName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("autostart: write desktop entry: %w", err)
	}
	return nil
}

// Disable removes the desktop entry file. No-op if already gone.
func Disable(appName string) error {
	path, err := desktopPath(appName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: remove desktop entry: %w", err)
	}
	return nil
}

// IsEnabled checks for the desktop entry file's existence.
func IsEnabled(appName string) bool {
	path, err := desktopPath(appName)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Path returns the desktop entry path for diagnostic display.
func Path(appName string) string {
	p, err := desktopPath(appName)
	if err != nil {
		return ""
	}
	return p
}

func desktopPath(appName string) (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("autostart: config dir: %w", err)
	}
	return filepath.Join(cfg, "autostart", appName+".desktop"), nil
}

// Hidden=false ensures the entry is shown in the user's autostart
// management UI (e.g., gnome-tweaks > Startup Applications).
const desktopTemplate = `[Desktop Entry]
Type=Application
Name=%s
Exec=%s
X-GNOME-Autostart-enabled=true
Hidden=false
Terminal=false
Comment=Autostart entry for %s
`
