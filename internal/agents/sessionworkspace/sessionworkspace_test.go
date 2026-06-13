package sessionworkspace

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func newLayout(t *testing.T) (agentconfig.Layout, string) {
	t.Helper()
	base := t.TempDir()
	sid := "sess-1"
	if err := os.MkdirAll(filepath.Join(base, "sessions", sid), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return agentconfig.NewLayout(base), sid
}

func TestIsInstanceID(t *testing.T) {
	if !IsInstanceID("sw_abc") {
		t.Error("sw_ id should be an instance id")
	}
	if IsInstanceID("conn-123") {
		t.Error("plain id should not be an instance id")
	}
}

func TestAddGetListRemove(t *testing.T) {
	layout, sid := newLayout(t)

	// Empty session → empty list, no file.
	if got, err := List(layout, sid); err != nil || len(got) != 0 {
		t.Fatalf("empty list: got %v err %v", got, err)
	}

	in, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "Staging"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !IsInstanceID(in.ID) {
		t.Fatalf("minted id %q not an instance id", in.ID)
	}

	got, ok, err := Get(layout, sid, in.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.BaseKey != "httprest" || got.Label != "Staging" {
		t.Fatalf("get mismatch: %+v", got)
	}

	// Missing base_key rejected.
	if _, err := Add(layout, sid, Instance{Label: "no base"}); err == nil {
		t.Error("expected error for missing base_key")
	}

	removed, err := Remove(layout, sid, in.ID)
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	// File should be gone once empty.
	if _, statErr := os.Stat(layout.SessionWorkspace(sid)); !os.IsNotExist(statErr) {
		t.Error("workspace file should be removed when empty")
	}
}

func TestSetConfigMergesAndPersists(t *testing.T) {
	layout, sid := newLayout(t)
	in, err := Add(layout, sid, Instance{BaseKey: "httprest"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := SetConfig(layout, sid, in.ID, map[string]string{"base_url": "https://a"}); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	if err := SetConfig(layout, sid, in.ID, map[string]string{"token": "wick_cenc_x"}); err != nil {
		t.Fatalf("set 2: %v", err)
	}

	// Reload from disk (fresh Load) to prove persistence + merge.
	got, ok, err := Get(layout, sid, in.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Config["base_url"] != "https://a" || got.Config["token"] != "wick_cenc_x" {
		t.Fatalf("config not merged/persisted: %+v", got.Config)
	}

	// Setting an unknown instance errors.
	if err := SetConfig(layout, sid, "sw_missing", map[string]string{"k": "v"}); err == nil {
		t.Error("expected error setting config on missing instance")
	}
}
