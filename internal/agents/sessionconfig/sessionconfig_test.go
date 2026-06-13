package sessionconfig

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func testLayout(t *testing.T) (agentconfig.Layout, string) {
	t.Helper()
	base := t.TempDir()
	layout := agentconfig.Layout{BaseDir: base}
	sid := "sess1"
	if err := os.MkdirAll(layout.SessionDir(sid), 0o755); err != nil {
		t.Fatal(err)
	}
	return layout, sid
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	layout, sid := testLayout(t)
	got, err := Load(layout, sid)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestSetMergesAndPersists(t *testing.T) {
	layout, sid := testLayout(t)
	if err := Set(layout, sid, "conn1", map[string]string{"base_url": "https://abc.net"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Set(layout, sid, "conn1", map[string]string{"token": "wick_enc_x"}); err != nil {
		t.Fatalf("Set 2: %v", err)
	}
	m, err := For(layout, sid, "conn1")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if m["base_url"] != "https://abc.net" || m["token"] != "wick_enc_x" {
		t.Fatalf("merge lost keys: %v", m)
	}
}

func TestClearSubsetAndAll(t *testing.T) {
	layout, sid := testLayout(t)
	_ = Set(layout, sid, "conn1", map[string]string{"a": "1", "b": "2"})

	removed, err := Clear(layout, sid, "conn1", []string{"a", "missing"})
	if err != nil {
		t.Fatalf("Clear subset: %v", err)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Fatalf("removed = %v, want [a]", removed)
	}

	removed, err = Clear(layout, sid, "conn1", nil)
	if err != nil {
		t.Fatalf("Clear all: %v", err)
	}
	if len(removed) != 1 || removed[0] != "b" {
		t.Fatalf("removed = %v, want [b]", removed)
	}
	// File should be gone once every override is cleared.
	if _, err := os.Stat(filepath.Join(layout.SessionDir(sid), "config_overrides.json")); !os.IsNotExist(err) {
		t.Fatalf("overrides file should be removed, stat err = %v", err)
	}
}

func TestClearUnknownConnectorIsNoop(t *testing.T) {
	layout, sid := testLayout(t)
	removed, err := Clear(layout, sid, "nope", nil)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if removed != nil {
		t.Fatalf("removed = %v, want nil", removed)
	}
}
