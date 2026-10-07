package opencode

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

func TestOpencodeAssetSelection(t *testing.T) {
	cases := map[managedbin.Host]string{
		{OS: "linux", Arch: "x64", AVX2: true}:             "opencode-linux-x64.tar.gz",
		{OS: "linux", Arch: "x64"}:                         "opencode-linux-x64-baseline.tar.gz",
		{OS: "linux", Arch: "x64", Musl: true, AVX2: true}: "opencode-linux-x64-musl.tar.gz",
		{OS: "linux", Arch: "x64", Musl: true}:             "opencode-linux-x64-baseline-musl.tar.gz",
		{OS: "linux", Arch: "arm64"}:                       "opencode-linux-arm64.tar.gz",
		{OS: "linux", Arch: "arm64", Musl: true}:           "opencode-linux-arm64-musl.tar.gz",
		{OS: "darwin", Arch: "arm64"}:                      "opencode-darwin-arm64.zip",
		{OS: "linux", Arch: "arm64", Termux: true}:         "opencode-linux-arm64.tar.gz", // PT_INTERP rewritten to glibc-runner's loader at install
	}
	for h, want := range cases {
		got, err := assetName(h)
		if err != nil || got != want {
			t.Errorf("%+v → %q %v, want %q", h, got, err, want)
		}
	}
	// the desktop app is never picked
	_, err := releaseSource{}.PickAsset(managedbin.Host{OS: "linux", Arch: "x64", AVX2: true},
		[]managedbin.Asset{{Name: "opencode-desktop-linux-x86_64.AppImage"}})
	if err == nil {
		t.Fatal("desktop asset accepted")
	}
}

func TestOpencodeUnpackTarGz(t *testing.T) {
	dir := t.TempDir()
	arch := filepath.Join(dir, "a.tar.gz")
	f, _ := os.Create(arch)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho 1.18.33\n")
	_ = tw.WriteHeader(&tar.Header{Name: "opencode", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()
	f.Close()
	dest := filepath.Join(dir, "opencode")
	if err := (releaseSource{}).Unpack(arch, dest); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(body) {
		t.Fatal("member content")
	}
	if v, ok := (releaseSource{}).Contract().Parse("1.18.33\n"); !ok || v != "1.18.33" {
		t.Fatal(v)
	}
}
