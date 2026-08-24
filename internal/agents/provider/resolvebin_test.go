package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinName returns a name that is executable on this OS, since Windows
// resolves bare names through PATHEXT rather than a permission bit.
func fakeBinName(stem string) string {
	if runtime.GOOS == "windows" {
		return stem + ".bat"
	}
	return stem
}

// writeFakeBin drops an executable file into dir and returns its full path.
func writeFakeBin(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	return p
}

// TestResolveBin_PrefersConfiguredBinary: an explicit override wins outright,
// so an operator who points wick at a specific build is not second-guessed by
// a PATH entry of the same name.
func TestResolveBin_PrefersConfiguredBinary(t *testing.T) {
	dir := t.TempDir()
	want := writeFakeBin(t, dir, fakeBinName("my-claude"))

	got, err := ResolveBin(Instance{Type: TypeClaude, Binary: want})
	if err != nil {
		t.Fatalf("ResolveBin: %v", err)
	}
	if got != want {
		t.Errorf("resolved %q, want the configured Binary %q", got, want)
	}
}

// TestResolveBin_FindsOnPath is the ordinary case: no override, the CLI is on
// PATH.
func TestResolveBin_FindsOnPath(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, fakeBinName(string(TypeClaude)))
	t.Setenv("PATH", dir)

	got, err := ResolveBin(Instance{Type: TypeClaude})
	if err != nil {
		t.Fatalf("ResolveBin: %v", err)
	}
	if got == "" {
		t.Fatal("resolved to empty path")
	}
}

// TestResolveBin_ReportsPathErrorWhenMissing pins the failure message.
//
// The error is what an operator sees in the UI (the AI paste parser surfaces it
// verbatim), so it has to name the binary that could not be found rather than
// report a scan-internal problem. This is the case that produced
// `exec: "claude": executable file not found in $PATH`.
func TestResolveBin_ReportsPathErrorWhenMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: nothing resolvable

	_, err := ResolveBin(Instance{Type: Type("definitely-not-installed")})
	if err == nil {
		t.Fatal("expected an error when the binary is nowhere")
	}
	if !strings.Contains(err.Error(), "definitely-not-installed") {
		t.Errorf("error %q does not name the binary; an operator cannot act on it", err)
	}
}

// TestResolveBin_DoesNotRunVersionProbe: ResolveBin sits on the call path, so
// it must not shell out to --version. A binary that exists but exits non-zero
// still resolves — liveness is Probe's job, not this function's.
func TestResolveBin_DoesNotRunVersionProbe(t *testing.T) {
	dir := t.TempDir()
	name := fakeBinName(string(TypeClaude))
	p := filepath.Join(dir, name)
	// A file that is executable but not a valid program: running it would fail,
	// resolving it must not.
	if err := os.WriteFile(p, []byte("\x00\x01not a program"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", dir)

	if _, err := ResolveBin(Instance{Type: TypeClaude}); err != nil {
		t.Errorf("ResolveBin failed on an unrunnable file (%v); it should resolve "+
			"the path and leave execution to the caller", err)
	}
}
