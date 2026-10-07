package omp

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

func TestOMPAssetSelection(t *testing.T) {
	cases := map[managedbin.Host]string{
		{OS: "linux", Arch: "x64", AVX2: true}:     "omp-linux-x64",
		{OS: "linux", Arch: "x64"}:                 "omp-linux-x64",
		{OS: "linux", Arch: "arm64"}:               "omp-linux-arm64",
		{OS: "linux", Arch: "x64", Musl: true}:     "omp-linux-musl-x64",
		{OS: "linux", Arch: "arm64", Musl: true}:   "omp-linux-musl-arm64",
		{OS: "darwin", Arch: "arm64"}:              "omp-darwin-arm64",
		{OS: "linux", Arch: "arm64", Termux: true}: "omp-linux-arm64", // PT_INTERP rewritten to glibc-runner's loader at install
	}
	for h, want := range cases {
		got, err := assetName(h)
		if err != nil || got != want {
			t.Errorf("%+v → %q %v, want %q", h, got, err, want)
		}
	}
	if _, err := assetName(managedbin.Host{OS: "linux", Arch: "riscv64"}); err == nil {
		t.Error("unsupported arch accepted")
	}
	a, err := releaseSource{}.PickAsset(managedbin.Host{OS: "linux", Arch: "x64"}, []managedbin.Asset{{Name: "omp-linux-arm64"}, {Name: "omp-linux-x64"}})
	if err != nil || a.Name != "omp-linux-x64" {
		t.Fatal(a, err)
	}
}

func TestOMPVersionAndSums(t *testing.T) {
	if v, ok := parseVersion("omp/18.4.3\n"); !ok || v != "18.4.3" {
		t.Fatal(v)
	}
	sums := []byte("aa  LICENSE\nafcecd  omp-linux-x64\nbb *omp-linux-arm64\n")
	if s, ok := sumsLookup(sums, "omp-linux-x64"); !ok || s != "afcecd" {
		t.Fatal(s)
	}
	if s, ok := sumsLookup(sums, "omp-linux-arm64"); !ok || s != "bb" {
		t.Fatal(s)
	}
	if _, ok := sumsLookup(sums, "omp-linux-musl-x64"); ok {
		t.Fatal("phantom entry")
	}
}
