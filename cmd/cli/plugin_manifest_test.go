package cli

import (
	"encoding/json"
	"strings"
	"testing"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// TestParseDumpedManifestService covers a kind=service dump: its key lives
// under service.meta (no "module" block), and the folder-name rule must still
// read it.
func TestParseDumpedManifestService(t *testing.T) {
	m, err := wickplugin.BuildSelfServiceManifest(wickplugin.ServiceModule{
		Meta: wickplugin.ToolMeta{Key: "runner_x", Name: "Runner X"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"module"`) {
		t.Fatalf("service dump carries a module block: %s", raw)
	}
	got, err := parseDumpedManifest(raw, "service/runner_x")
	if err != nil {
		t.Fatalf("service dump rejected: %v", err)
	}
	if got.Kind != wickplugin.KindService || got.Module.Meta.Key != "runner_x" || got.Service == nil {
		t.Fatalf("parsed: kind=%q key=%q service=%v", got.Kind, got.Module.Meta.Key, got.Service)
	}
	if _, err := parseDumpedManifest(raw, "service/other"); err == nil || !strings.Contains(err.Error(), `"runner_x" must equal the folder name "other"`) {
		t.Fatalf("folder mismatch not caught: %v", err)
	}
}
