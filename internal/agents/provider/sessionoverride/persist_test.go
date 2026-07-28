package sessionoverride

import (
	"os"
	"path/filepath"
	"testing"
)

// withSessionDir points the store at a temp sessions root and restores the
// memory-only default afterwards, so persistence tests don't leak into others.
func withSessionDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	SetSessionDirResolver(func(id string) string {
		if id == "" {
			return ""
		}
		return filepath.Join(root, id)
	})
	t.Cleanup(func() { SetSessionDirResolver(nil) })
	return root
}

// A toggle must land in overrides.json inside the session dir — next to
// meta.json / agents.json — so it survives the engine goroutine exiting.
func TestSetWritesSidecar(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-persist"
	if err := os.MkdirAll(filepath.Join(root, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	defer Clear(sid)

	Set(sid, "thinking_on", "true")

	path := filepath.Join(root, sid, "overrides.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sidecar not written to session dir: %v", err)
	}
	if got := string(b); got != `{"thinking_on":"true"}` {
		t.Fatalf("unexpected sidecar contents: %s", got)
	}
}

// The actual reported bug: the value must come back after the in-memory store is
// gone (engine reap / wick restart), not reset to false.
func TestValuesSurviveProcessRestart(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-restart"
	if err := os.MkdirAll(filepath.Join(root, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	defer Clear(sid)

	Set(sid, "thinking_on", "true")
	Set(sid, "reasoning_effort", "high")

	// Simulate a fresh process: wipe the in-memory map + load marks entirely.
	storeMu.Lock()
	store = map[string]map[string]string{}
	loaded = map[string]bool{}
	storeMu.Unlock()

	vals := Values(sid)
	if vals["thinking_on"] != "true" {
		t.Errorf("thinking_on lost across restart: %+v", vals)
	}
	if vals["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort lost across restart: %+v", vals)
	}
}

// Setting a second key must merge onto the persisted first one, not replace the
// file wholesale (the popover auto-saves one field at a time).
func TestSetMergesOntoPersisted(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-merge"
	if err := os.MkdirAll(filepath.Join(root, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	defer Clear(sid)

	Set(sid, "thinking_on", "true")

	storeMu.Lock() // forget everything in memory between the two writes
	store = map[string]map[string]string{}
	loaded = map[string]bool{}
	storeMu.Unlock()

	Set(sid, "reasoning_effort", "low")

	vals := Values(sid)
	if vals["thinking_on"] != "true" {
		t.Errorf("first key clobbered by second write: %+v", vals)
	}
	if vals["reasoning_effort"] != "low" {
		t.Errorf("second key not saved: %+v", vals)
	}
}

// Clear is the explicit reset: it must delete the sidecar so the value does not
// come back on the next read.
func TestClearRemovesSidecar(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-clear"
	if err := os.MkdirAll(filepath.Join(root, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, sid, "overrides.json")

	Set(sid, "thinking_on", "true")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: sidecar should exist: %v", err)
	}

	Clear(sid)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("sidecar still present after Clear (err=%v)", err)
	}
	if v := Values(sid)["thinking_on"]; v != "" {
		t.Fatalf("value resurrected after Clear: %q", v)
	}
}

// Turning a toggle back off must persist the "false", not delete the file and
// silently fall back to the provider baseline (which could be ON).
func TestExplicitFalsePersists(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-false"
	if err := os.MkdirAll(filepath.Join(root, sid), 0o755); err != nil {
		t.Fatal(err)
	}
	defer Clear(sid)

	Set(sid, "thinking_on", "false")

	storeMu.Lock()
	store = map[string]map[string]string{}
	loaded = map[string]bool{}
	storeMu.Unlock()

	if v := Values(sid)["thinking_on"]; v != "false" {
		t.Fatalf("explicit off not persisted, got %q", v)
	}
}

// A corrupt sidecar must read as "no overrides" rather than wedging the session.
func TestMalformedSidecarIgnored(t *testing.T) {
	root := withSessionDir(t)
	sid := "sess-corrupt"
	dir := filepath.Join(root, sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "overrides.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Values(sid); len(got) != 0 {
		t.Fatalf("malformed sidecar should read as empty, got %+v", got)
	}
}

// With no resolver wired (tests, embedders without a layout) the store must keep
// working purely in memory and touch no disk.
func TestMemoryOnlyWithoutResolver(t *testing.T) {
	SetSessionDirResolver(nil)
	sid := "sess-mem"
	defer Clear(sid)

	Set(sid, "thinking_on", "true")
	if v := Values(sid)["thinking_on"]; v != "true" {
		t.Fatalf("memory-only store broken: %q", v)
	}
}

// A session whose dir does not exist yet must not panic or lose the in-memory
// value — the write just can't be persisted.
func TestMissingSessionDirStillSetsInMemory(t *testing.T) {
	withSessionDir(t) // resolver points at a dir we never create
	sid := "sess-nodir"
	defer Clear(sid)

	Set(sid, "thinking_on", "true")
	if v := Values(sid)["thinking_on"]; v != "true" {
		t.Fatalf("value lost when session dir absent: %q", v)
	}
}
