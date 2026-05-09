package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveGateBinary_EnvOverrideWins confirms GATE_BIN short-
// circuits all other lookups — including missing files. The env var
// is the user's explicit pointer; we trust it as-is and let the
// spawn fail loudly later if it's wrong.
func TestResolveGateBinary_EnvOverrideWins(t *testing.T) {
	t.Setenv(envOverride, "/totally/made/up/path/gate")
	got, err := ResolveGateBinary(t.TempDir())
	if err != nil {
		t.Fatalf("ResolveGateBinary: %v", err)
	}
	if got != "/totally/made/up/path/gate" {
		t.Errorf("got %q, want env value", got)
	}
}

// TestSiblingGateBinary_FoundNextToExecutable verifies the dev /
// installer fallback: a gate sidecar file dropped next to the
// running binary is picked up without env or embed.
//
// We can't move the test binary, so we plant a file matching the
// expected name beside it, look up via the helper, then clean up.
// Skipped if the binary is in a read-only location.
func TestSiblingGateBinary_FoundNextToExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable not available: %v", err)
	}
	name := brandedGateName()
	planted := filepath.Join(filepath.Dir(exe), name)

	// Skip if the file already exists — don't trample on a real install.
	if _, err := os.Stat(planted); err == nil {
		t.Skip("gate already next to test binary; skipping plant test")
	}

	if err := os.WriteFile(planted, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Skipf("can't write next to test binary (read-only?): %v", err)
	}
	defer os.Remove(planted)

	got := siblingGateBinary()
	if got != planted {
		t.Errorf("siblingGateBinary: got %q, want %q", got, planted)
	}
}

func TestSiblingGateBinary_AbsentReturnsEmpty(t *testing.T) {
	// Without planting, the helper should return "". In rare CI
	// setups the test binary's neighbour might already have a gate
	// binary (e.g. dev box with PATH-installed gate); in that case
	// the helper legitimately returns it — skip rather than fail.
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), brandedGateName())); err == nil {
		t.Skip("gate already exists next to test binary")
	}
	if got := siblingGateBinary(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// suppress unused import warning when runtime no longer used in
// this file directly.
var _ = runtime.GOOS
