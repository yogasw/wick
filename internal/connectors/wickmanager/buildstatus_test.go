package wickmanager

import (
	"testing"
	"time"
)

func TestNextPatch(t *testing.T) {
	for in, want := range map[string]string{"0.1.692": "0.1.693", "v1.9": "1.10", "dev": "", "": "", "1.x": ""} {
		if got := nextPatch(in); got != want {
			t.Errorf("nextPatch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildStatus(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	out := buildStatus("1.2.3", nil, now)
	if out["next_version"] != "1.2.4" || out["deploy"] != nil {
		t.Fatalf("no state: %v", out)
	}
	raw := []byte(`{"old":"1.2.3","new":"1.2.4","next":"1.2.5","title":"x","phase":"build","updated_at":"2026-01-02T03:03:05Z","dir":"/ignored"}`)
	d := buildStatus("1.2.3", raw, now)["deploy"].(map[string]any)
	if d["running"] != true || d["to"] != "1.2.4" || d["updated_ago"] != "1m0s" {
		t.Fatalf("running deploy: %v", d)
	}
	if _, ok := d["dir"]; ok {
		t.Error("host path must not be exposed")
	}
	d = buildStatus("1.2.4", []byte(`{"new":"1.2.4","phase":"live"}`), now)["deploy"].(map[string]any)
	if d["running"] != false {
		t.Errorf("live deploy must not be running: %v", d)
	}
	d = buildStatus("1.2.3", []byte(`not json`), now)["deploy"].(map[string]any)
	if d["error"] == nil {
		t.Errorf("bad json must be reported: %v", d)
	}
}
