package memscope

import (
	"os"
	"path/filepath"
)

// ensureSliceAt writes the slice unit into dir when its content differs
// from what is already there. Reports whether it wrote.
//
// Split from EnsureSlice, and kept off the build-tagged files, so the
// write logic is exercised by the test suite on every platform rather
// than only where systemd exists. Duplicating it per platform would let
// the two copies drift.
func ensureSliceAt(dir string, l SliceLimits) (bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	target := filepath.Join(dir, SliceName)
	want := RenderSlice(l)

	if cur, err := os.ReadFile(target); err == nil && string(cur) == want {
		return false, nil
	}
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
