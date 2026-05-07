//go:build windows

package updater

import (
	"fmt"
	"runtime"
	"strings"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-<version>-windows-<arch>.msi
//
// CI publishes the per-user MSI (built via wixl) as the windows release
// artifact. Updater stages the MSI as-is and applies it via msiexec —
// the MSI's MajorUpgrade element rewrites the installed .exe in place.
// Version is the release tag (e.g. "v0.1.9") with the leading "v"
// stripped so it matches the filename emitted by `wick build`.
func (u *Updater) assetName(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return fmt.Sprintf("%s-%s-windows-%s.msi", u.appName, v, runtime.GOARCH)
}

// extractStaged is a pass-through on Windows — the MSI is what
// msiexec consumes; no inner-binary extraction needed.
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	return asset, nil
}

// stagedExt is the file extension for the staged update file on disk.
// Windows keeps the .msi; msiexec /i requires the .msi extension.
func stagedExt() string { return ".msi" }
