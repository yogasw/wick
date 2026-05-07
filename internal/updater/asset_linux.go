//go:build linux

package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/blakesmith/ar"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-<version>-linux-<arch>.deb
//
// Version is the release tag (e.g. "v0.1.9") with the leading "v"
// stripped to match the filename emitted by `wick build`.
func (u *Updater) assetName(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return fmt.Sprintf("%s-%s-linux-%s.deb", u.appName, v, runtime.GOARCH)
}

// stagedExt is the file extension for the staged update file on disk.
// On Linux we keep the .deb so swapLinuxDeb can hand it to dpkg via
// pkexec, which preserves package-manager bookkeeping (dpkg -l, apt
// upgrade) instead of bypassing dpkg with a raw binary swap. The
// previous design extracted the inner ELF and renamed it onto
// /usr/bin/<app>; that "worked" for a single update but desynced
// dpkg's database, so the next `apt upgrade` would silently overwrite
// the user's running version with whatever the distro mirror had.
func stagedExt() string { return ".deb" }

// extractStaged is now a pass-through — the .deb is what dpkg consumes.
// The legacy ar-extract path is preserved as extractInnerBinary for
// the rare case where dpkg/pkexec is unavailable and we need to fall
// back to a raw binary swap (e.g. minimal containers).
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	return asset, nil
}

// extractInnerBinary peels back the .deb to its inner binary:
//
//	.deb (ar archive) → data.tar.gz → ./usr/bin/<app>
//
// Pure-Go — no system dpkg required. Used by swapLinuxDeb's fallback
// path when dpkg is missing.
func extractInnerBinary(asset []byte, appName string) ([]byte, error) {
	r := ar.NewReader(bytes.NewReader(asset))
	for {
		hdr, err := r.Next()
		if err == io.EOF {
			return nil, errors.New("data.tar.gz not found in deb")
		}
		if err != nil {
			return nil, fmt.Errorf("ar next: %w", err)
		}
		if hdr.Name != "data.tar.gz" {
			continue
		}
		dataGz, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("read data.tar.gz: %w", err)
		}
		return extractBinaryFromTarGz(dataGz, appName)
	}
}

func extractBinaryFromTarGz(data []byte, appName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	target := "./usr/bin/" + appName
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in deb data.tar.gz", target)
		}
		if err != nil {
			return nil, fmt.Errorf("tar next: %w", err)
		}
		if hdr.Name == target || hdr.Name == "usr/bin/"+appName {
			return io.ReadAll(tr)
		}
	}
}
