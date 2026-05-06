//go:build windows

package updater

import (
	"fmt"
	"runtime"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-windows-<arch>.exe
func (u *Updater) assetName() string {
	return fmt.Sprintf("%s-windows-%s.exe", u.appName, runtime.GOARCH)
}

// extractStaged is a pass-through on Windows — the downloaded .exe
// IS the binary, no archive to crack open.
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	return asset, nil
}
