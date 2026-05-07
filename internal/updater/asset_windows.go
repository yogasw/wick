//go:build windows

package updater

import (
	"fmt"
	"runtime"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-windows-<arch>.msi
//
// CI publishes the per-user MSI (built via wixl) as the windows release
// artifact. Updater stages the MSI as-is and applies it via msiexec —
// the MSI's MajorUpgrade element rewrites the installed .exe in place.
func (u *Updater) assetName() string {
	return fmt.Sprintf("%s-windows-%s.msi", u.appName, runtime.GOARCH)
}

// extractStaged is a pass-through on Windows — the MSI is what
// msiexec consumes; no inner-binary extraction needed.
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	return asset, nil
}

// stagedExt is the file extension for the staged update file on disk.
// Windows keeps the .msi; msiexec /i requires the .msi extension.
func stagedExt() string { return ".msi" }
