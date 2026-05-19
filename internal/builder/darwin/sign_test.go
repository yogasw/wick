package darwin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSignAdHoc_SkipNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin host: covered by TestSignAdHoc_Darwin")
	}
	if err := SignAdHoc(filepath.Join(t.TempDir(), "fake.app")); err != ErrSkippedSign {
		t.Fatalf("want ErrSkippedSign on %s, got %v", runtime.GOOS, err)
	}
}

func TestSignAdHoc_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires darwin host (codesign)")
	}

	appPath := filepath.Join(t.TempDir(), "Foo.app")
	macOSDir := filepath.Join(appPath, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(macOSDir, "Foo")
	if err := seedDummyBin("/bin/ls", binPath); err != nil {
		t.Fatalf("seed dummy bin: %v", err)
	}

	if err := SignAdHoc(appPath); err != nil {
		t.Fatalf("first sign: %v", err)
	}
	assertAdHocSigned(t, appPath)

	// --force makes reruns idempotent (matches CI rerun behavior).
	if err := SignAdHoc(appPath); err != nil {
		t.Fatalf("second sign (idempotency): %v", err)
	}
	assertAdHocSigned(t, appPath)
}

func assertAdHocSigned(t *testing.T, appPath string) {
	t.Helper()
	out, err := exec.Command("codesign", "-dvv", appPath).CombinedOutput()
	if err != nil {
		t.Fatalf("codesign -dvv: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Signature=adhoc") {
		t.Fatalf("expected Signature=adhoc in:\n%s", out)
	}
}

func seedDummyBin(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}
