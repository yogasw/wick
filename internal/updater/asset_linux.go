//go:build linux

package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blakesmith/ar"
)

// installDirWritable reports whether the directory holding the running
// binary can be written by the current user — the precondition for an
// in-place binary swap. Self-update never escalates privilege (pkexec/
// sudo): it mirrors install.sh, which installs unprivileged to a
// user-owned location (Termux $PREFIX/bin, ~/.local/bin) and tells the
// user to re-run with sudo themselves when a system path is involved.
// So if the install dir isn't user-writable, Apply fails with a clear
// message rather than silently prompting for a password.
func installDirWritable(exePath string) bool {
	dir := filepath.Dir(exePath)
	f, err := os.CreateTemp(dir, ".wick-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// materializeInnerBinary extracts the inner ELF from a staged .deb and
// writes it to a sibling file so swapUnix can rename + exec it. Used by
// the binary-swap path (Termux / no-dpkg) where the .deb itself can't
// be handed to a package manager. Returns the path to the extracted
// binary.
func (u *Updater) materializeInnerBinary(debPath string) (string, error) {
	data, err := os.ReadFile(debPath)
	if err != nil {
		return "", fmt.Errorf("read staged deb: %w", err)
	}
	bin, err := extractInnerBinary(data, u.appName)
	if err != nil {
		return "", err
	}
	out := strings.TrimSuffix(debPath, ".deb")
	if err := os.WriteFile(out, bin, 0o755); err != nil {
		return "", fmt.Errorf("write extracted binary: %w", err)
	}
	return out, nil
}

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
// The release asset is a .deb, so we stage it as-is; ApplyStagedAndRestart
// peels out the inner ELF (extractInnerBinary) and swaps it in place. We
// deliberately do NOT shell out to dpkg: self-update never escalates
// privilege (no pkexec/sudo), mirroring install.sh, and Termux's dpkg
// remaps the .deb's ./usr layout anyway — a direct binary swap is both
// simpler and correct across Termux / ~/.local/bin / containers.
func stagedExt() string { return ".deb" }

// extractStaged is a pass-through at download time — the .deb is kept
// intact on disk. The inner ELF is extracted later, at apply time, by
// materializeInnerBinary (which calls extractInnerBinary).
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
	return asset, nil
}

// extractInnerBinary peels back the .deb to its inner binary:
//
//	.deb (ar archive) → data.tar.gz → ./usr/bin/<app>
//
// Pure-Go — no system dpkg required. This is the apply mechanism on
// Linux/Termux: extract, then rename+exec in place via swapUnix.
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
