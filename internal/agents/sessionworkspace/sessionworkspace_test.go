package sessionworkspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
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

// writeMeta plants a session meta.json with the given status + last-active
// time so the sweeper's idle check has something to read.
func writeMeta(t *testing.T, layout agentconfig.Layout, sid string, status session.Status, lastActive time.Time) {
	t.Helper()
	if err := os.MkdirAll(layout.SessionDir(sid), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := session.SaveMeta(layout, sid, session.Meta{
		Origin:     session.OriginUI,
		Status:     status,
		CreatedAt:  lastActive,
		LastActive: lastActive,
	}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
}

func TestReapAllWritesTombstonesAndDropsConfig(t *testing.T) {
	layout, sid := newLayout(t)
	if _, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "Staging"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := SetConfig(layout, sid, mustFirstID(t, layout, sid), map[string]string{"token": "wick_cenc_x"}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	n, err := reapAll(layout, sid, "session idle", time.Now())
	if err != nil || n != 1 {
		t.Fatalf("reapAll: n=%d err=%v", n, err)
	}
	// Instances gone, tombstone left, no config on the tombstone.
	if got, _ := List(layout, sid); len(got) != 0 {
		t.Fatalf("instances should be gone after reap, got %+v", got)
	}
	tombs, _ := Tombstones(layout, sid)
	if len(tombs) != 1 || tombs[0].Label != "Staging" || tombs[0].BaseKey != "httprest" {
		t.Fatalf("expected one tombstone for Staging, got %+v", tombs)
	}
	if tombs[0].Reason != "session idle" || tombs[0].DeletedAt == "" {
		t.Fatalf("tombstone missing reason/deleted_at: %+v", tombs[0])
	}
}

func TestReCreateClearsTombstone(t *testing.T) {
	layout, sid := newLayout(t)
	if _, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "Staging"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := reapAll(layout, sid, "session idle", time.Now()); err != nil {
		t.Fatalf("reapAll: %v", err)
	}
	// Re-create with the same label+base clears the matching tombstone.
	if _, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "Staging"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if tombs, _ := Tombstones(layout, sid); len(tombs) != 0 {
		t.Fatalf("re-create should clear the matching tombstone, got %+v", tombs)
	}
}

func TestTombstoneOnlyFileSurvives(t *testing.T) {
	layout, sid := newLayout(t)
	if _, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "X"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := reapAll(layout, sid, "session idle", time.Now()); err != nil {
		t.Fatalf("reapAll: %v", err)
	}
	// File must NOT be deleted when only tombstones remain.
	if _, statErr := os.Stat(layout.SessionWorkspace(sid)); statErr != nil {
		t.Fatalf("workspace file should survive with tombstones only: %v", statErr)
	}
}

func TestSweepReapsIdleKeepsActive(t *testing.T) {
	layout, _ := newLayout(t)
	now := time.Now()

	// idle-old: idle + last active long ago → reaped.
	writeMeta(t, layout, "idle-old", session.StatusIdle, now.Add(-IdleGrace-time.Minute))
	if _, err := Add(layout, "idle-old", Instance{BaseKey: "httprest", Label: "A"}); err != nil {
		t.Fatalf("add idle-old: %v", err)
	}
	// idle-fresh: idle but active recently → kept (within grace).
	writeMeta(t, layout, "idle-fresh", session.StatusIdle, now.Add(-time.Minute))
	if _, err := Add(layout, "idle-fresh", Instance{BaseKey: "httprest", Label: "B"}); err != nil {
		t.Fatalf("add idle-fresh: %v", err)
	}
	// running-old: running, old last-active → kept (active work).
	writeMeta(t, layout, "running-old", session.StatusRunning, now.Add(-2*IdleGrace))
	if _, err := Add(layout, "running-old", Instance{BaseKey: "httprest", Label: "C"}); err != nil {
		t.Fatalf("add running-old: %v", err)
	}

	remaining, err := sweepOnce(layout, now)
	if err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	// idle-fresh (1) + running-old (1) survive; idle-old reaped.
	if remaining != 2 {
		t.Fatalf("expected 2 live remaining, got %d", remaining)
	}
	if got, _ := List(layout, "idle-old"); len(got) != 0 {
		t.Errorf("idle-old should be reaped, got %+v", got)
	}
	if tombs, _ := Tombstones(layout, "idle-old"); len(tombs) != 1 {
		t.Errorf("idle-old should have a tombstone, got %+v", tombs)
	}
	if got, _ := List(layout, "idle-fresh"); len(got) != 1 {
		t.Errorf("idle-fresh should be kept, got %+v", got)
	}
	if got, _ := List(layout, "running-old"); len(got) != 1 {
		t.Errorf("running-old should be kept, got %+v", got)
	}
}

func TestSweepFiresReapNotify(t *testing.T) {
	layout, _ := newLayout(t)
	now := time.Now()
	writeMeta(t, layout, "idle-old", session.StatusIdle, now.Add(-IdleGrace-time.Minute))
	if _, err := Add(layout, "idle-old", Instance{BaseKey: "httprest", Label: "Staging"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	var gotSID string
	var gotTombs []Tombstone
	SetReapNotify(func(sid string, tombs []Tombstone) {
		gotSID = sid
		gotTombs = tombs
	})
	defer SetReapNotify(nil) // don't leak the hook into other tests

	if _, err := sweepOnce(layout, now); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if gotSID != "idle-old" {
		t.Fatalf("reapNotify session = %q, want idle-old", gotSID)
	}
	if len(gotTombs) != 1 || gotTombs[0].Label != "Staging" {
		t.Fatalf("reapNotify tombstones = %+v, want one for Staging", gotTombs)
	}
}

func TestSweepNoReapNotifyWhenNothingReaped(t *testing.T) {
	layout, _ := newLayout(t)
	now := time.Now()
	// Fresh idle session within grace → not reaped → no notify.
	writeMeta(t, layout, "idle-fresh", session.StatusIdle, now.Add(-time.Minute))
	if _, err := Add(layout, "idle-fresh", Instance{BaseKey: "httprest", Label: "X"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	called := false
	SetReapNotify(func(string, []Tombstone) { called = true })
	defer SetReapNotify(nil)

	if _, err := sweepOnce(layout, now); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if called {
		t.Fatal("reapNotify should not fire when nothing was reaped")
	}
}

func TestSweepReapsSessionWithNoMeta(t *testing.T) {
	// A workspace whose session meta.json is gone (session deleted) is reaped —
	// its instances must not outlive the session.
	layout, sid := newLayout(t) // newLayout makes the dir but no meta.json
	if _, err := Add(layout, sid, Instance{BaseKey: "httprest", Label: "orphan"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	remaining, err := sweepOnce(layout, time.Now())
	if err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("orphan instance should be reaped, remaining=%d", remaining)
	}
}

func mustFirstID(t *testing.T, layout agentconfig.Layout, sid string) string {
	t.Helper()
	got, err := List(layout, sid)
	if err != nil || len(got) == 0 {
		t.Fatalf("no instance to resolve: err=%v", err)
	}
	return got[0].ID
}
