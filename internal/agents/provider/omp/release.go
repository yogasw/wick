package omp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

// release.go is omp's managed-binary source: GitHub can1357/oh-my-pi,
// raw binary assets named like the official installer builds them
// (omp.sh/install): omp-<platform>-<arch>, platform "linux" or
// "linux-musl" (alpine / ldd reports musl), arch x64|arm64. Each release
// also ships SHA256SUMS.txt, cross-checked on top of the API digest.

func init() {
	managedbin.Register("omp", releaseSource{})
}

type releaseSource struct{}

func (releaseSource) Binary() string { return "omp" }
func (releaseSource) Repo() string   { return "can1357/oh-my-pi" }

// assetName mirrors omp-install.sh: BINARY="omp-${PLATFORM}-${ARCH}".
func assetName(h managedbin.Host) (string, error) {
	if h.Arch != "x64" && h.Arch != "arm64" {
		return "", fmt.Errorf("omp publishes no build for %s", h.Arch)
	}
	switch h.OS {
	case "linux":
		if h.Musl {
			return "omp-linux-musl-" + h.Arch, nil
		}
		return "omp-linux-" + h.Arch, nil
	case "darwin":
		return "omp-darwin-" + h.Arch, nil
	}
	return "", fmt.Errorf("managed omp is not supported on %s", h.OS)
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

// Unpack: the asset is the binary itself.
func (releaseSource) Unpack(downloaded, dest string) error { return os.Rename(downloaded, dest) }

// CrossCheck requires SHA256SUMS.txt to list the asset with the same sum.
func (releaseSource) CrossCheck(ctx context.Context, f managedbin.Fetcher, rel managedbin.Release, asset managedbin.Asset, sum string) error {
	var sums *managedbin.Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "SHA256SUMS.txt" {
			sums = &rel.Assets[i]
		}
	}
	if sums == nil {
		return errors.New("release has no SHA256SUMS.txt")
	}
	b, err := f.FetchSmall(ctx, sums.URL, 256<<10)
	if err != nil {
		return err
	}
	listed, ok := sumsLookup(b, asset.Name)
	if !ok {
		return fmt.Errorf("SHA256SUMS.txt does not list %s", asset.Name)
	}
	if !strings.EqualFold(listed, sum) {
		return fmt.Errorf("SHA256SUMS.txt says %s for %s, download is %s", listed, asset.Name, sum)
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

var ompVersionRe = regexp.MustCompile(`omp/(\d+\.\d+\.\d+[0-9A-Za-z.\-+]*)`)

// Contract: `omp --version` prints "omp/18.4.3".
func (releaseSource) Contract() managedbin.VersionContract {
	return managedbin.VersionContract{Args: []string{"--version"}, Parse: parseVersion}
}

func parseVersion(out string) (string, bool) {
	if m := ompVersionRe.FindStringSubmatch(out); m != nil {
		return m[1], true
	}
	return managedbin.FirstSemver(out)
}
