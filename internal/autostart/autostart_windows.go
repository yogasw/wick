//go:build windows

package autostart

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enable writes HKCU\...\Run\<appName> = "<quoted exe path>".
func Enable(appName string) error {
	exe := currentExe()
	if exe == "" {
		return errors.New("autostart: cannot resolve current binary path")
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	// Quote the path so paths with spaces work as a single token.
	value := `"` + exe + `"`
	if err := k.SetStringValue(appName, value); err != nil {
		return fmt.Errorf("autostart: set value: %w", err)
	}
	return nil
}

// Disable removes the value if present. No error when already absent.
func Disable(appName string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(appName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("autostart: delete value: %w", err)
	}
	return nil
}

// IsEnabled reports true when HKCU\...\Run has a value named appName.
func IsEnabled(appName string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	return err == nil
}

// Path returns the registry path for diagnostic display.
func Path(appName string) string {
	return `HKCU\` + runKey + `\` + appName
}
