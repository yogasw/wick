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

	"github.com/blakesmith/ar"
)

// assetName returns the release asset name for this OS/arch:
//
//	<app>-linux-<arch>.deb
func (u *Updater) assetName() string {
	return fmt.Sprintf("%s-linux-%s.deb", u.appName, runtime.GOARCH)
}

// extractStaged peels back the .deb to its inner binary:
//
//	.deb (ar archive) → data.tar.gz → ./usr/bin/<app>
//
// Pure-Go — no system dpkg required.
func (u *Updater) extractStaged(asset []byte) ([]byte, error) {
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
		return extractBinaryFromTarGz(dataGz, u.appName)
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
