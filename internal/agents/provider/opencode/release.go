package opencode

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

// release.go is opencode's managed-binary source: GitHub
// anomalyco/opencode, CLI assets named like opencode.ai/install builds
// them: opencode-<os>-<arch>[-baseline][-musl].<tar.gz|zip> — baseline
// for x64 without AVX2, musl for alpine / ldd musl, tar.gz on linux and
// zip elsewhere. The archive holds one `opencode` binary. The
// opencode-desktop-* assets are the desktop app, never picked.

func init() {
	managedbin.Register("opencode", releaseSource{})
}

type releaseSource struct{}

func (releaseSource) Binary() string { return "opencode" }
func (releaseSource) Repo() string   { return "anomalyco/opencode" }

// assetName mirrors opencode-install.sh (target + archive_ext).
func assetName(h managedbin.Host) (string, error) {
	switch h.OS + "-" + h.Arch {
	case "linux-x64", "linux-arm64", "darwin-x64", "darwin-arm64":
	default:
		return "", fmt.Errorf("opencode publishes no CLI build for %s-%s", h.OS, h.Arch)
	}
	target := h.OS + "-" + h.Arch
	if h.Arch == "x64" && !h.AVX2 {
		target += "-baseline"
	}
	if h.OS == "linux" && h.Musl {
		target += "-musl"
	}
	ext := ".zip"
	if h.OS == "linux" {
		ext = ".tar.gz"
	}
	return "opencode-" + target + ext, nil
}

func (releaseSource) PickAsset(h managedbin.Host, assets []managedbin.Asset) (managedbin.Asset, error) {
	want, err := assetName(h)
	if err != nil {
		return managedbin.Asset{}, err
	}
	for _, a := range assets {
		if a.Name == want {
			return a, nil
		}
	}
	return managedbin.Asset{}, fmt.Errorf("release has no %s asset", want)
}

// Unpack extracts the `opencode` member (at any depth) to dest, capped at
// the download limit so a hostile archive cannot fill the disk.
func (releaseSource) Unpack(downloaded, dest string) error {
	f, err := os.Open(downloaded)
	if err != nil {
		return err
	}
	defer f.Close()
	var head [2]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if head[0] == 0x1f && head[1] == 0x8b {
		return untarMember(f, "opencode", dest)
	}
	if head[0] == 'P' && head[1] == 'K' {
		st, err := f.Stat()
		if err != nil {
			return err
		}
		return unzipMember(f, st.Size(), "opencode", dest)
	}
	return errors.New("unknown archive format")
}

func writeMember(r io.Reader, dest string) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, managedbin.MaxDownloadBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > managedbin.MaxDownloadBytes {
		err = errors.New("archive member exceeds the size limit")
	}
	return err
}

func untarMember(r io.Reader, name, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("archive has no %s", name)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == name {
			return writeMember(tr, dest)
		}
	}
}

func unzipMember(r io.ReaderAt, size int64, name, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		if !zf.FileInfo().IsDir() && path.Base(zf.Name) == name {
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			return writeMember(rc, dest)
		}
	}
	return fmt.Errorf("archive has no %s", name)
}

// CrossCheck: opencode publishes no checksum file; the API digest is it.
func (releaseSource) CrossCheck(context.Context, managedbin.Fetcher, managedbin.Release, managedbin.Asset, string) error {
	return nil
}

// Contract: `opencode --version` prints the bare version ("1.18.33").
func (releaseSource) Contract() managedbin.VersionContract {
	return managedbin.VersionContract{Args: []string{"--version"}, Parse: managedbin.FirstSemver}
}
