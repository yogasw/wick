//go:build darwin

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-darwin-<arch>.dmg
func (u *Updater) assetName() string {
	return fmt.Sprintf("%s-darwin-%s.dmg", u.appName, runtime.GOARCH)
}

// stagedExt is the file extension for the staged update file on disk.
// Darwin extracts the inner Mach-O binary from the .dmg, so no ext.
func stagedExt() string { return "" }

// extractStaged mounts the downloaded .dmg via hdiutil, copies the
// inner Contents/MacOS/<app> binary out, and detaches. The whole .app
// shell (Info.plist, icns) stays untouched at the install location —
// only the binary at Contents/MacOS/<app> needs swapping.
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	tmpDmg, err := os.CreateTemp("", "wick-update-*.dmg")
	if err != nil {
		return nil, fmt.Errorf("temp dmg: %w", err)
	}
	tmpPath := tmpDmg.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpDmg.Write(asset); err != nil {
		tmpDmg.Close()
		return nil, fmt.Errorf("write temp dmg: %w", err)
	}
	tmpDmg.Close()

	mountPoint, err := os.MkdirTemp("", "wick-update-mount-*")
	if err != nil {
		return nil, fmt.Errorf("mount dir: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	attach := exec.Command("hdiutil", "attach",
		"-nobrowse", "-readonly",
		"-mountpoint", mountPoint,
		tmpPath,
	)
	if out, err := attach.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("hdiutil attach: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		detach := exec.Command("hdiutil", "detach", "-quiet", mountPoint)
		_ = detach.Run()
	}()

	binPath, err := findInnerBinary(mountPoint, u.appName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(binPath)
	if err != nil {
		return nil, fmt.Errorf("read inner binary: %w", err)
	}
	return data, nil
}

// findInnerBinary walks the mounted DMG looking for
// <app>.app/Contents/MacOS/<app>. Returns the first match; errors
// when no .app bundle is present.
func findInnerBinary(mountPoint, appName string) (string, error) {
	expected := filepath.Join(mountPoint, appName+".app", "Contents", "MacOS", appName)
	if _, err := os.Stat(expected); err == nil {
		return expected, nil
	}
	// Fallback: walk for any *.app/Contents/MacOS/<exe> in case the
	// bundle name differs from the binary name.
	var found string
	walkErr := filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !info.IsDir() && strings.Contains(path, ".app/Contents/MacOS/") && info.Mode()&0o111 != 0 {
			found = path
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("walk dmg: %w", walkErr)
	}
	if found == "" {
		return "", errors.New("no .app/Contents/MacOS/<binary> found in dmg")
	}
	return found, nil
}
