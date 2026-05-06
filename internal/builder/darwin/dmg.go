package darwin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// ErrSkippedDMG is returned when DMG creation is skipped because the
// host OS is not darwin (hdiutil is macOS-only). Callers may treat
// this as a non-fatal warning.
var ErrSkippedDMG = errors.New("dmg: hdiutil unavailable on non-darwin host")

// PackageDMG wraps an existing .app bundle into a UDZO-compressed
// .dmg disk image via the macOS `hdiutil` tool. Returns the .dmg
// path on success; returns ErrSkippedDMG when the host is not darwin
// (the .app bundle alone is still useful in that case).
//
// volName is the visible name when the user mounts the disk image;
// dmgPath is the output filename (typically <app>-darwin-<arch>.dmg).
func PackageDMG(appPath, dmgPath, volName string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", ErrSkippedDMG
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		return "", fmt.Errorf("hdiutil not found: %w", err)
	}
	// hdiutil refuses to overwrite by default; remove any prior file
	// to keep CI reruns idempotent.
	_ = os.Remove(dmgPath)

	cmd := exec.Command("hdiutil", "create",
		"-volname", volName,
		"-srcfolder", appPath,
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
