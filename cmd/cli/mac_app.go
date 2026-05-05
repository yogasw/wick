package cli

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackmordaunt/icns/v3"

	"github.com/yogasw/wick/internal/systemtray"
)

// packageMacApp wraps a built darwin binary into a standard .app bundle:
//
//	bin/<app>.app/
//	├── Contents/
//	│   ├── Info.plist
//	│   ├── MacOS/<app>          (the binary)
//	│   └── Resources/icon.icns  (rendered from the brand W)
//
// Returns the bundle root path. binPath is moved into Contents/MacOS/<app>;
// caller doesn't need to clean it up separately.
func packageMacApp(binPath, appName, appVersion, bundleID string) (string, error) {
	bundleRoot := filepath.Join(filepath.Dir(binPath), appName+".app")
	contents := filepath.Join(bundleRoot, "Contents")
	macOS := filepath.Join(contents, "MacOS")
	resources := filepath.Join(contents, "Resources")
	for _, d := range []string{macOS, resources} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// Move the freshly built binary into Contents/MacOS/<app>.
	dstBin := filepath.Join(macOS, appName)
	if err := os.Rename(binPath, dstBin); err != nil {
		return "", fmt.Errorf("move binary into bundle: %w", err)
	}
	if err := os.Chmod(dstBin, 0o755); err != nil {
		return "", fmt.Errorf("chmod binary: %w", err)
	}

	// Render brand icon → PNG → .icns. icns library wants an image.Image,
	// so we decode the PNG bytes from systemtray.BrandIcon.
	pngBytes := systemtray.BrandIcon(false)
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", fmt.Errorf("decode brand png: %w", err)
	}
	icnsPath := filepath.Join(resources, "icon.icns")
	if err := writeICNS(icnsPath, img); err != nil {
		return "", fmt.Errorf("write icns: %w", err)
	}

	// Info.plist — minimum keys macOS needs to treat the bundle as an app.
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
	// Strip leading "v" so CFBundleShortVersionString stays clean (e.g. 1.2.3 not v1.2.3).
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

