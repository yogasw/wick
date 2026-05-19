package darwin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// ErrSkippedSign is returned when ad-hoc codesign is skipped because
// the host OS is not darwin (codesign is macOS-only). Callers may
// treat this as a non-fatal warning — the resulting .app is still
// usable for developers building locally, just unsigned.
var ErrSkippedSign = errors.New("codesign: skipped on non-darwin host")

// SignAdHoc applies an ad-hoc signature to a .app bundle. Without
// this, an unsigned binary plus the com.apple.quarantine xattr that
// macOS attaches to anything downloaded over the network produces a
// hard "<app> is damaged and can't be opened" dialog on Apple Silicon
// — and unlike the milder "unidentified developer" warning, that one
// has no right-click → Open override path.
//
// Ad-hoc signing (codesign -s -) is not Developer ID and does NOT
// substitute for notarization. Users still see the unidentified-dev
// warning on first launch, but they can bypass it via right-click →
// Open or System Settings → Privacy & Security → Open Anyway. No
// terminal required.
//
// --deep walks nested executables inside the bundle (Contents/MacOS/
// <App>, <App>-gate) so the gate sidecar is signed too. --force
// overwrites any prior signature so reruns in CI are idempotent.
func SignAdHoc(appPath string) error {
	if runtime.GOOS != "darwin" {
		return ErrSkippedSign
	}
	if _, err := exec.LookPath("codesign"); err != nil {
		return fmt.Errorf("codesign not found: %w", err)
	}
	cmd := exec.Command("codesign", "--sign", "-", "--deep", "--force", "--timestamp=none", appPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codesign --sign -: %w", err)
	}
	return nil
}
