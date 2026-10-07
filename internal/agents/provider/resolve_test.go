package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeExecutable drops an executable file the OS's LookPath accepts.
func fakeExecutable(t *testing.T, dir string) string {
	t.Helper()
	name := "fake-cli"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveBinaryExplicitOverrideFound(t *testing.T) {
	bin := fakeExecutable(t, t.TempDir())
	path, found := ResolveBinary(Instance{Type: TypeClaude, Name: "x", Binary: bin})
	if !found || path != bin {
		t.Fatalf("path=%q found=%v, want %q true", path, found, bin)
	}
}

func TestResolveBinaryExplicitOverrideMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.exe")
	path, found := ResolveBinary(Instance{Type: TypeClaude, Name: "x", Binary: missing})
	if found {
		t.Fatal("missing override must not be found")
	}
	if path != missing {
		t.Fatalf("path = %q, want override echoed for UI display", path)
	}
}
