package plugin

import (
	"path/filepath"
	"testing"
)

func TestDefaultDirHonorsEnvOverride(t *testing.T) {
	t.Setenv("WICK_PLUGINS_DIR", "/tmp/custom-plugins")
	if got := DefaultDir(); got != "/tmp/custom-plugins" {
		t.Fatalf("env override ignored: %s", got)
	}
}

func TestDefaultDirFallsBackToHome(t *testing.T) {
	t.Setenv("WICK_PLUGINS_DIR", "")
	got := DefaultDir()
	if filepath.Base(got) != "connectors" {
		t.Fatalf("unexpected default dir: %s", got)
	}
}

func TestRunDirHonorsEnvOverride(t *testing.T) {
	t.Setenv("WICK_PLUGIN_SOCKET_DIR", "/tmp/custom-run")
	if got := RunDir(); got != "/tmp/custom-run" {
		t.Fatalf("env override ignored: %s", got)
	}
}

func TestRunDirFallsBackToHome(t *testing.T) {
	t.Setenv("WICK_PLUGIN_SOCKET_DIR", "")
	if filepath.Base(RunDir()) != "run" {
		t.Fatalf("unexpected default run dir: %s", RunDir())
	}
}

// DataDirFor must be the dir an installed binary derives for itself
// (wickplugin.DataDir): <plugins root>/<key>, alongside connectors/.
func TestDataDirForMatchesInstallLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	t.Setenv("WICK_PLUGINS_DIR", "")
	t.Setenv("WICK_PLUGINS_ROOT", root)

	exe := filepath.Join(DefaultDir(), "playwright_browser", "playwright_browser")
	fromExe := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(exe))), "playwright_browser")
	if got := DataDirFor("playwright_browser"); got != fromExe || got != filepath.Join(root, "playwright_browser") {
		t.Fatalf("DataDirFor = %q, want %q", got, fromExe)
	}
}
