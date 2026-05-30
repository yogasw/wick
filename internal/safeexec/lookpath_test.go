//go:build !windows

// Windows has no unix exec bit, so findExecutable's mode&0o111 check
// rejects every file. These tests assume unix semantics — gate them off
// on Windows. safeexec itself works on Windows; only this test fixture
// is unix-specific.

package safeexec

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLookPath_AbsoluteExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := LookPath(bin)
	if err != nil {
		t.Fatalf("LookPath(%q): %v", bin, err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

func TestLookPath_AbsoluteNotExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LookPath(bin)
	if err == nil {
		t.Fatal("expected error for non-executable file")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("want fs.ErrPermission, got %v", err)
	}
}

func TestLookPath_OnPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	got, err := LookPath("tool")
	if err != nil {
		t.Fatalf("LookPath(\"tool\"): %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

func TestLookPath_NotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := LookPath("definitely-not-a-real-binary-xyz")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("want exec.ErrNotFound, got %v", err)
	}
}

func TestLookPath_Directory(t *testing.T) {
	dir := t.TempDir()
	_, err := LookPath(dir)
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestResolveBin_AbsolutePassthrough(t *testing.T) {
	// Resolve must not validate or alter paths with a slash — the
	// whole point is to avoid touching the file system (and therefore
	// faccessat2) when the caller already gave us a full path.
	want := "/opt/not/actually/here/claude"
	got, err := ResolveBin(want)
	if err != nil {
		t.Fatalf("ResolveBin(%q): %v", want, err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveBin_BareName(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got, err := ResolveBin("claude")
	if err != nil {
		t.Fatalf("ResolveBin: %v", err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}
