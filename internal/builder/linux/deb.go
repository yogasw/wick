// Package linux handles Linux-specific build steps — wrapping the
// raw binary into a Debian binary package (.deb). Pure-Go ar + tar
// implementation means no system dpkg-deb is required and the package
// can be cross-built from any host.
package linux

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blakesmith/ar"

	"github.com/yogasw/wick/internal/systemtray"
)

// PackageDeb builds a Debian binary package from a freshly compiled
// Linux binary. Layout inside the .deb:
//
//	usr/bin/<app>                                                   (the binary)
//	usr/share/icons/hicolor/256x256/apps/<app>.png                   (brand icon)
//	usr/share/icons/hicolor/1024x1024/apps/<app>.png                 (brand icon)
//	usr/share/applications/<app>.desktop                             (.desktop entry)
//	DEBIAN/control                                                   (package metadata)
//
// .deb format: ar archive containing debian-binary (text "2.0\n"),
// control.tar.gz (DEBIAN/*), data.tar.gz (the rest of the filesystem).
//
// Output path is <dir-of-binPath>/<app>-linux-<arch>.deb — kept
// consistent with mac (.dmg) and windows (.exe) naming so the
// self-updater can resolve assets with one rule.
func PackageDeb(binPath, appName, appVersion, goarch string) (string, error) {
	debArch := mapGoArchToDeb(goarch)
	ver := strings.TrimPrefix(appVersion, "v")
	debPath := filepath.Join(filepath.Dir(binPath), fmt.Sprintf("%s-%s-linux-%s.deb", appName, ver, goarch))

	binBytes, err := os.ReadFile(binPath)
	if err != nil {
		return "", fmt.Errorf("read binary: %w", err)
	}
	iconPNG := systemtray.BrandIcon(false)

	dataTarGz, err := buildDataTarGz(appName, binBytes, iconPNG)
	if err != nil {
		return "", fmt.Errorf("data.tar.gz: %w", err)
	}
	controlTarGz, err := buildControlTarGz(appName, ver, debArch, len(binBytes)+len(iconPNG))
	if err != nil {
		return "", fmt.Errorf("control.tar.gz: %w", err)
	}

	deb, err := os.Create(debPath)
	if err != nil {
		return "", fmt.Errorf("create deb: %w", err)
	}
	defer deb.Close()

	w := ar.NewWriter(deb)
	if err := w.WriteGlobalHeader(); err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if err := writeARMember(w, "debian-binary", []byte("2.0\n"), now); err != nil {
		return "", err
	}
	if err := writeARMember(w, "control.tar.gz", controlTarGz, now); err != nil {
		return "", err
	}
	if err := writeARMember(w, "data.tar.gz", dataTarGz, now); err != nil {
		return "", err
	}
	return debPath, nil
}

func writeARMember(w *ar.Writer, name string, data []byte, mtime int64) error {
	hdr := &ar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0o644,
		ModTime: time.Unix(mtime, 0),
	}
	if err := w.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func buildDataTarGz(appName string, binBytes, iconPNG []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	now := time.Now()
	dirs := []string{
		"./usr/",
		"./usr/bin/",
		"./usr/share/",
		"./usr/share/applications/",
		"./usr/share/icons/",
		"./usr/share/icons/hicolor/",
		"./usr/share/icons/hicolor/256x256/",
		"./usr/share/icons/hicolor/256x256/apps/",
		"./usr/share/icons/hicolor/1024x1024/",
		"./usr/share/icons/hicolor/1024x1024/apps/",
	}
	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     d,
			Mode:     0o755,
			Typeflag: tar.TypeDir,
			ModTime:  now,
		}); err != nil {
			return nil, err
		}
	}

	files := []struct {
		path string
		data []byte
		mode int64
	}{
		{"./usr/bin/" + appName, binBytes, 0o755},
		{"./usr/share/icons/hicolor/256x256/apps/" + appName + ".png", iconPNG, 0o644},
		{"./usr/share/icons/hicolor/1024x1024/apps/" + appName + ".png", iconPNG, 0o644},
		{"./usr/share/applications/" + appName + ".desktop", []byte(buildDesktop(appName)), 0o644},
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:    f.path,
			Mode:    f.mode,
			Size:    int64(len(f.data)),
			ModTime: now,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildControlTarGz(appName, ver, debArch string, installedSizeBytes int) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	now := time.Now()
	if err := tw.WriteHeader(&tar.Header{
		Name:     "./",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
		ModTime:  now,
	}); err != nil {
		return nil, err
	}
	control := buildControlFile(appName, ver, debArch, installedSizeBytes)
	if err := tw.WriteHeader(&tar.Header{
		Name:    "./control",
		Mode:    0o644,
		Size:    int64(len(control)),
		ModTime: now,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(control)); err != nil {
		return nil, err
	}
	for _, script := range []struct {
		name string
		body string
	}{
		{"postinst", buildPostinstScript()},
		{"postrm", buildPostrmScript(appName)},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name:    "./" + script.name,
			Mode:    0o755,
			Size:    int64(len(script.body)),
			ModTime: now,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(script.body)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildControlFile(appName, ver, debArch string, installedSizeBytes int) string {
	kb := (installedSizeBytes + 1023) / 1024
	return fmt.Sprintf(`Package: %s
Version: %s
Section: utils
Priority: optional
Architecture: %s
Installed-Size: %d
Maintainer: %s <noreply@example.com>
Description: %s
 Built with wick.
`, appName, ver, debArch, kb, appName, appName)
}

func buildPostinstScript() string {
	return `#!/bin/sh
set -e
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
exit 0
`
}

func buildPostrmScript(appName string) string {
	q := shellQuote(appName)
	return fmt.Sprintf(`#!/bin/sh
set -e
app=%[1]s
if [ "$1" = "remove" ] || [ "$1" = "purge" ]; then
	rm -f "/etc/xdg/autostart/$app.desktop"
	rm -f "/root/.config/autostart/$app.desktop"
	for home in /home/*; do
		[ -d "$home" ] || continue
		rm -f "$home/.config/autostart/$app.desktop"
	done
fi
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
exit 0
`, q)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func buildDesktop(appName string) string {
	return fmt.Sprintf(`[Desktop Entry]
Version=1.0
Type=Application
Name=%s
Exec=%s
Icon=%s
Terminal=false
Categories=Utility;
StartupNotify=false
`, appName, appName, appName)
}

// mapGoArchToDeb translates Go's GOARCH names to dpkg's architecture
// names.
func mapGoArchToDeb(goarch string) string {
	switch goarch {
	case "amd64":
		return "amd64"
	case "386":
		return "i386"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	default:
		return goarch
	}
}
