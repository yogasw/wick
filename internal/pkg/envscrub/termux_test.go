package envscrub

import (
	"os"
	"slices"
	"testing"
)

const (
	tGlibcLibc = "/data/data/com.termux/files/usr/glibc/lib/libc.so"
	tGlibcLib  = "/data/data/com.termux/files/usr/glibc/lib"
	tExecSO    = "/data/data/com.termux/files/usr/lib/libtermux-exec.so"
)

func TestScrubDropsTermuxGlibcLoader(t *testing.T) {
	reset(t, "")
	in := []string{
		"PATH=/data/data/com.termux/files/usr/bin",
		"LD_PRELOAD=" + tGlibcLibc,
		"HOME=/data/data/com.termux/files/home",
		"LD_LIBRARY_PATH=" + tGlibcLib,
	}
	got := Scrub(in)
	want := []string{in[0], in[2]}
	if !slices.Equal(got, want) {
		t.Fatalf("Scrub = %q, want %q", got, want)
	}
}

func TestScrubKeepsTermuxExecPreload(t *testing.T) {
	reset(t, "")
	in := []string{"LD_PRELOAD=" + tExecSO + ":" + tGlibcLibc}
	got := Scrub(in)
	if !slices.Equal(got, []string{"LD_PRELOAD=" + tExecSO}) {
		t.Fatalf("Scrub = %q", got)
	}
}

func TestScrubKeepsLoaderOutsideTermux(t *testing.T) {
	reset(t, "")
	in := []string{"LD_PRELOAD=/opt/glibc/lib/libc.so", "LD_LIBRARY_PATH=/usr/local/lib"}
	if got := Scrub(in); !slices.Equal(got, in) {
		t.Fatalf("Scrub = %q, want unchanged", got)
	}
}

func TestScrubDropsTermuxGlibcLoaderWithDenyDisabled(t *testing.T) {
	reset(t, "-")
	in := []string{"LD_PRELOAD=" + tGlibcLibc, "DATABASE_URL=x"}
	if got := Scrub(in); !slices.Equal(got, []string{"DATABASE_URL=x"}) {
		t.Fatalf("Scrub = %q", got)
	}
}

func TestScrubProcessLoaderEnv(t *testing.T) {
	t.Setenv("LD_PRELOAD", tExecSO+" "+tGlibcLibc)
	t.Setenv("LD_LIBRARY_PATH", tGlibcLib)
	ScrubProcessLoaderEnv()
	if v := os.Getenv("LD_PRELOAD"); v != tExecSO {
		t.Fatalf("LD_PRELOAD = %q", v)
	}
	if _, ok := os.LookupEnv("LD_LIBRARY_PATH"); ok {
		t.Fatal("LD_LIBRARY_PATH still set")
	}
}
