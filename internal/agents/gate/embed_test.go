package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSiblingGateBinary_FoundNextToExecutable verifies the
// production resolution path: a gate sidecar file dropped next to
// the running binary is picked up first (before embed extract).
// `wick build` ships the sidecar in every installer, so this is
// the path real installs take.
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

var _ = runtime.GOOS
