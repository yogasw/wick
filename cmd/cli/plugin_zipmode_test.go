package cli

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// A release zip must be world-readable even under a strict umask, with the
// binary executable and every other entry plain 0644.
func TestZipPluginFilesModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	bin := filepath.Join(dir, "sample")
	if err := os.WriteFile(bin, []byte("bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sample-1.0.0-linux-amd64.zip")
	if err := zipPluginFiles(dst, map[string]string{"sample": bin, "plugin.json": manifest}); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o644 {
		t.Fatalf("zip mode = %o, want 644", got)
	}

	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	want := map[string]os.FileMode{"sample": 0o755, "plugin.json": 0o644}
	for _, f := range zr.File {
		if got := f.Mode().Perm(); got != want[f.Name] {
			t.Errorf("%s mode = %o, want %o", f.Name, got, want[f.Name])
		}
	}
}
