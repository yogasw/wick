package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanDirNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names, err := ScanDirNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("unexpected names: %v", names)
	}
}

func TestPathExists(t *testing.T) {
	dir := t.TempDir()
	if !PathExists(dir) {
		t.Fatal("dir should exist")
	}
	if PathExists(filepath.Join(dir, "missing")) {
		t.Fatal("non-existent path returned true")
	}
}
