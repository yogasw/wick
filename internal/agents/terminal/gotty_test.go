package terminal

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

// Asset names as published on sorenisanerd/gotty v1.8.0.
var gottyAssets = []managedbin.Asset{
	{Name: "gotty_v1.8.0_darwin_amd64.tar.gz"},
	{Name: "gotty_v1.8.0_darwin_arm64.tar.gz"},
	{Name: "gotty_v1.8.0_freebsd_amd64.tar.gz"},
	{Name: "gotty_v1.8.0_linux_386.tar.gz"},
	{Name: "gotty_v1.8.0_linux_amd64.tar.gz"},
	{Name: "gotty_v1.8.0_linux_arm.tar.gz"},
	{Name: "gotty_v1.8.0_linux_arm64.tar.gz"},
	{Name: "LICENSE"},
	{Name: "SHA256SUMS", URL: "https://github.com/sorenisanerd/gotty/releases/download/v1.8.0/SHA256SUMS"},
}

func TestGottyRegistered(t *testing.T) {
	src, ok := managedbin.Lookup(GottyType)
	if !ok {
		t.Fatal("gotty is not a registered managed type")
	}
	if src.Repo() != "sorenisanerd/gotty" || src.Binary() != "gotty" {
		t.Fatalf("repo/binary = %s/%s", src.Repo(), src.Binary())
	}
}

func TestGottyPickAsset(t *testing.T) {
	cases := []struct {
		h    managedbin.Host
		want string
	}{
		{managedbin.Host{OS: "linux", Arch: "x64"}, "gotty_v1.8.0_linux_amd64.tar.gz"},
		{managedbin.Host{OS: "linux", Arch: "x64", Musl: true}, "gotty_v1.8.0_linux_amd64.tar.gz"}, // static Go binary
		{managedbin.Host{OS: "linux", Arch: "arm64"}, "gotty_v1.8.0_linux_arm64.tar.gz"},
		{managedbin.Host{OS: "darwin", Arch: "arm64"}, "gotty_v1.8.0_darwin_arm64.tar.gz"},
	}
	for _, c := range cases {
		a, err := gottySource{}.PickAsset(c.h, gottyAssets)
		if err != nil || a.Name != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.h.Label(), a.Name, err, c.want)
		}
	}
	if _, err := (gottySource{}).PickAsset(managedbin.Host{OS: "windows", Arch: "x64"}, gottyAssets); err == nil {
		t.Error("windows has no gotty build; want an error")
	}
	if _, err := (gottySource{}).PickAsset(managedbin.Host{OS: "linux", Arch: "x64"}, gottyAssets[:2]); err == nil {
		t.Error("missing linux asset should be an error")
	}
}

type fakeFetcher map[string]string

func (f fakeFetcher) FetchSmall(_ context.Context, url string, _ int64) ([]byte, error) {
	return []byte(f[url]), nil
}

func TestGottyCrossCheck(t *testing.T) {
	const sum = "9cf032e1f3a49d33da3ba32c79f49892aad94e52edc6417524a76b623ced2f5f"
	rel := managedbin.Release{Tag: "v1.8.0", Assets: gottyAssets}
	asset := managedbin.Asset{Name: "gotty_v1.8.0_linux_amd64.tar.gz"}
	f := fakeFetcher{gottyAssets[8].URL: "fcef4efc  gotty_v1.8.0_linux_arm64.tar.gz\n" + sum + "  gotty_v1.8.0_linux_amd64.tar.gz\n"}
	if err := (gottySource{}).CrossCheck(context.Background(), f, rel, asset, strings.ToUpper(sum)); err != nil {
		t.Fatalf("matching sum rejected: %v", err)
	}
	if err := (gottySource{}).CrossCheck(context.Background(), f, rel, asset, strings.Repeat("0", 64)); err == nil {
		t.Fatal("mismatching sum accepted")
	}
	if err := (gottySource{}).CrossCheck(context.Background(), f, rel, managedbin.Asset{Name: "gotty_x.tar.gz"}, sum); err == nil {
		t.Fatal("unlisted asset accepted")
	}
	if err := (gottySource{}).CrossCheck(context.Background(), f, managedbin.Release{Assets: gottyAssets[:8]}, asset, sum); err == nil {
		t.Fatal("release without SHA256SUMS accepted")
	}
}

func TestGottyUnpack(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "g.tgz")
	f, _ := os.Create(arc)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho gotty version v1.8.0\n")
	_ = tw.WriteHeader(&tar.Header{Name: "./gotty", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()
	f.Close()
	dest := filepath.Join(dir, "out")
	if err := (gottySource{}).Unpack(arc, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != string(body) {
		t.Fatalf("unpacked %q", b)
	}
}

func TestGottyContract(t *testing.T) {
	c := gottySource{}.Contract()
	v, ok := c.Parse("gotty version v1.8.0\n")
	if !ok || v != "1.8.0" || !managedbin.MatchesTag("v1.8.0", v) {
		t.Fatalf("parse = %q %v", v, ok)
	}
	if strings.Join(c.Args, " ") != "--version" {
		t.Fatalf("args = %v", c.Args)
	}
}
