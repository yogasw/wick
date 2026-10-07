package terminal

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

// gotty.go is gotty's managed-binary source: GitHub sorenisanerd/gotty
// (MIT), assets named gotty_<tag>_<os>_<goarch>.tar.gz holding one
// `gotty` binary, plus a SHA256SUMS file the download is cross-checked
// against on top of the API digest. Same install/verify/rollback flow as
// omp and opencode; nothing downloads until an admin presses the button.

// GottyType is gotty's managedbin type key.
const GottyType = "gotty"

func init() {
	managedbin.Register(GottyType, gottySource{})
}

type gottySource struct{}

func (gottySource) Binary() string { return "gotty" }
func (gottySource) Repo() string   { return "sorenisanerd/gotty" }

// gottyArch maps managedbin's host arch to the GOARCH gotty's release
// names use.
func gottyArch(h managedbin.Host) (string, error) {
	switch h.OS + "-" + h.Arch {
	case "linux-x64", "darwin-x64":
		return h.OS + "_amd64", nil
	case "linux-arm64", "darwin-arm64":
		return h.OS + "_arm64", nil
	}
	return "", fmt.Errorf("gotty publishes no build wick uses for %s-%s", h.OS, h.Arch)
}

// PickAsset matches gotty_<any tag>_<os>_<arch>.tar.gz — the tag sits in
// the name, and the release's own tag is not passed here.
func (gottySource) PickAsset(h managedbin.Host, assets []managedbin.Asset) (managedbin.Asset, error) {
	suffix, err := gottyArch(h)
	if err != nil {
		return managedbin.Asset{}, err
	}
	suffix = "_" + suffix + ".tar.gz"
	for _, a := range assets {
		if strings.HasPrefix(a.Name, "gotty_") && strings.HasSuffix(a.Name, suffix) {
			return a, nil
		}
	}
	return managedbin.Asset{}, fmt.Errorf("release has no gotty_*%s asset", suffix)
}

// Unpack extracts the `gotty` member to dest, capped at the download
// limit so a hostile archive cannot fill the disk.
func (gottySource) Unpack(downloaded, dest string) error {
	f, err := os.Open(downloaded)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("archive has no gotty")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "gotty" {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, managedbin.MaxDownloadBytes+1))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil && n > managedbin.MaxDownloadBytes {
			err = errors.New("archive member exceeds the size limit")
		}
		return err
	}
}

// CrossCheck requires the release's SHA256SUMS to list the asset with the
// same sum.
func (gottySource) CrossCheck(ctx context.Context, f managedbin.Fetcher, rel managedbin.Release, asset managedbin.Asset, sum string) error {
	var sums *managedbin.Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "SHA256SUMS" {
			sums = &rel.Assets[i]
		}
	}
	if sums == nil {
		return errors.New("release has no SHA256SUMS")
	}
	b, err := f.FetchSmall(ctx, sums.URL, 256<<10)
	if err != nil {
		return err
	}
	listed, ok := sumsLookup(b, asset.Name)
	if !ok {
		return fmt.Errorf("SHA256SUMS does not list %s", asset.Name)
	}
	if !strings.EqualFold(listed, sum) {
		return fmt.Errorf("SHA256SUMS says %s for %s, download is %s", listed, asset.Name, sum)
	}
	return nil
}

// sumsLookup reads "<hex>  <name>" (or "<hex> *<name>") lines.
func sumsLookup(b []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0], true
		}
	}
	return "", false
}

// Contract: `gotty --version` prints "gotty version v1.8.0".
func (gottySource) Contract() managedbin.VersionContract {
	return managedbin.VersionContract{Args: []string{"--version"}, Parse: managedbin.FirstSemver}
}
