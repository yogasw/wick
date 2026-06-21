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
