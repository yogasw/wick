package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsHelperPreservesArgs verifies the generated update helper
// relaunches the post-install exe WITH the original subcommand + flags,
// so a headless `all` service re-serves after a Windows MSI update
// instead of dropping back to a no-arg (tray/idle) launch.
func TestWindowsHelperPreservesArgs(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "update-helper.bat")
	args := windowsArgString([]string{"all", "--host", "0.0.0.0"})

	if err := writeWindowsHelper(helper, filepath.Join(dir, "h.log"),
		`C:\Programs\app\app.exe`, filepath.Join(dir, "app.msi"),
		filepath.Join(dir, "msi.log"), args); err != nil {
		t.Fatalf("writeWindowsHelper: %v", err)
	}
	b, err := os.ReadFile(helper)
	if err != nil {
		t.Fatalf("read helper: %v", err)
	}
	script := string(b)

	want := `start "" "C:\Programs\app\app.exe" "all" "--host" "0.0.0.0"`
	if !strings.Contains(script, want) {
		t.Errorf("helper missing preserved-args launch line.\nwant substring: %s\ngot:\n%s", want, script)
	}
}

// TestWindowsArgStringEmpty ensures a no-arg launch still produces a
// valid (empty) suffix rather than a stray token.
func TestWindowsArgStringEmpty(t *testing.T) {
	if got := windowsArgString(nil); got != "" {
		t.Errorf("windowsArgString(nil) = %q, want empty", got)
	}
}
