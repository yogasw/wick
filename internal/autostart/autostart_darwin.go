//go:build darwin

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Enable writes ~/Library/LaunchAgents/<appName>.plist with RunAtLoad=true
// and a single ProgramArguments entry pointing at the current binary.
// launchd loads it automatically at the next user login; for an
// already-running session the user can launchctl load manually if they
// want it to take effect immediately.
func Enable(appName string) error {
	exe := currentExe()
	if exe == "" {
		return errors.New("autostart: cannot resolve current binary path")
	}
	path, err := plistPath(appName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: mkdir LaunchAgents: %w", err)
	}
	body := fmt.Sprintf(plistTemplate, escapeXML(appName), escapeXML(exe))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("autostart: write plist: %w", err)
	}
	return nil
}

// Disable removes the plist file. No-op if already gone.
func Disable(appName string) error {
	path, err := plistPath(appName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: remove plist: %w", err)
	}
	return nil
}

// IsEnabled checks for the plist file's existence.
func IsEnabled(appName string) bool {
	path, err := plistPath(appName)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Path returns the plist path for diagnostic display.
func Path(appName string) string {
	p, err := plistPath(appName)
	if err != nil {
		return ""
	}
	return p
}

func plistPath(appName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", appName+".plist"), nil
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`

func escapeXML(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			out = append(out, "&amp;"...)
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
