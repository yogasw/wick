// Package darwin handles macOS-specific build steps — wrapping the
// raw binary into a .app bundle and (host-darwin only) further
// wrapping that into a .dmg disk image. Pure-Go .app construction
// works from any host; .dmg requires the macOS-only `hdiutil` tool.
package darwin

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackmordaunt/icns/v3"

	"github.com/yogasw/wick/internal/systemtray"
)

// PackageApp wraps a built darwin binary into a standard .app bundle:
//
//	bin/<app>.app/
//	├── Contents/
//	│   ├── Info.plist
//	│   ├── MacOS/<app>          (the binary, copied)
//	│   └── Resources/icon.icns  (rendered from the brand W)
//
// Returns the bundle root path. The source binary at binPath is
// COPIED (not moved) so the raw artifact remains available for the
// self-updater and for naming-consistent CI uploads.
func PackageApp(binPath, appName, appVersion, bundleID string) (string, error) {
	bundleRoot := filepath.Join(filepath.Dir(binPath), appName+".app")
	contents := filepath.Join(bundleRoot, "Contents")
	macOS := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, d := range []string{macOS, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	dstBin := filepath.Join(macOS, appName)
	if err := copyFile(binPath, dstBin, 0o755); err != nil {
		return "", fmt.Errorf("copy binary into bundle: %w", err)
	}

	pngBytes := systemtray.BrandIcon(false)
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", fmt.Errorf("decode brand png: %w", err)
	}
	icnsPath := filepath.Join(resources, "icon.icns")
	if err := writeICNS(icnsPath, img); err != nil {
		return "", fmt.Errorf("write icns: %w", err)
	}

	plist := buildInfoPlist(appName, appVersion, bundleID)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		return "", fmt.Errorf("write Info.plist: %w", err)
	}
	return bundleRoot, nil
}

func writeICNS(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return icns.Encode(f, img)
}

func buildInfoPlist(appName, appVersion, bundleID string) string {
	year := time.Now().Year()
	ver := strings.TrimPrefix(appVersion, "v")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>%s</string>
	<key>CFBundleIconFile</key>
	<string>icon.icns</string>
	<key>CFBundleIdentifier</key>
	<string>%s</string>
	<key>CFBundleName</key>
	<string>%s</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>%s</string>
	<key>CFBundleVersion</key>
	<string>%s</string>
	<key>LSMinimumSystemVersion</key>
	<string>10.13</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSHumanReadableCopyright</key>
	<string>Copyright © %d %s</string>
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
`, appName, bundleID, appName, ver, ver, year, appName)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
