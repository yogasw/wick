package darwin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrSkippedDMG is returned when DMG creation is skipped because the
// host OS is not darwin (hdiutil is macOS-only). Callers may treat
// this as a non-fatal warning.
var ErrSkippedDMG = errors.New("dmg: hdiutil unavailable on non-darwin host")

// PackageDMG wraps an existing .app bundle into a UDZO-compressed
// .dmg disk image via the macOS `hdiutil` tool.
//
// When installerLayout is true, the image is staged so it also
// contains an "Applications" symlink — Finder then shows the
// standard "drag-to-install" layout when the user mounts it. When
// false, the .dmg embeds just the .app (current/legacy behavior).
//
// Returns the .dmg path on success; returns ErrSkippedDMG when the
// host is not darwin (the .app bundle alone is still useful then).
//
// volName is the visible name when the user mounts the disk image;
// dmgPath is the output filename (typically <app>-darwin-<arch>.dmg).
func PackageDMG(appPath, dmgPath, volName string, installerLayout bool) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", ErrSkippedDMG
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		return "", fmt.Errorf("hdiutil not found: %w", err)
	}

	srcFolder := appPath
	if installerLayout {
		stage, err := os.MkdirTemp("", "wick-dmg-*")
		if err != nil {
			return "", fmt.Errorf("dmg stage: %w", err)
		}
		defer os.RemoveAll(stage)

		stagedApp := filepath.Join(stage, filepath.Base(appPath))
		if err := exec.Command("cp", "-R", appPath, stagedApp).Run(); err != nil {
			return "", fmt.Errorf("stage app: %w", err)
		}
		if err := os.Symlink("/Applications", filepath.Join(stage, "Applications")); err != nil {
			return "", fmt.Errorf("applications symlink: %w", err)
		}
		srcFolder = stage
	}

	// hdiutil refuses to overwrite by default; remove any prior file
	// to keep CI reruns idempotent.
	_ = os.Remove(dmgPath)

	cmd := exec.Command("hdiutil", "create",
		"-volname", volName,
		"-srcfolder", srcFolder,
		"-ov",
		"-format", "UDZO",
		dmgPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("hdiutil create: %w", err)
	}
	return dmgPath, nil
}
