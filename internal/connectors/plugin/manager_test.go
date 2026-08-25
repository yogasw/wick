package plugin

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestManagerEvictsIdle(t *testing.T) {
	killed := map[string]bool{}
	m := &Manager{
		idleTimeout: 10 * time.Millisecond,
		entries:     map[string]*entry{},
		killFn:      func(key string) { killed[key] = true },
		now:         func() time.Time { return time.Unix(100, 0) },
	}
	m.entries["github"] = &entry{lastUsed: time.Unix(0, 0)}
	m.entries["slack"] = &entry{lastUsed: time.Unix(100, 0)}

	m.sweep()

	if !killed["github"] {
		t.Fatal("expected idle plugin github to be killed")
	}
	if killed["slack"] {
		t.Fatal("fresh plugin slack must not be killed")
	}
	if _, ok := m.entries["github"]; ok {
		t.Fatal("killed entry must be removed from the map")
	}
}

func TestIsPlugin(t *testing.T) {
	m := &Manager{binaries: map[string]string{"slack": "/x/slack"}}
	if !m.IsPlugin("slack") {
		t.Fatal("slack should be a plugin")
	}
	if m.IsPlugin("github") {
		t.Fatal("github is not a plugin")
	}
}

func TestKillAllIsIdempotent(t *testing.T) {
	m := NewManager(map[string]string{}, time.Minute)
	m.KillAll()
	m.KillAll() // must not panic on second call
}

func TestSetAndRemoveBinary(t *testing.T) {
	m := &Manager{
		entries:  map[string]*entry{},
		binaries: map[string]string{},
		now:      func() time.Time { return time.Unix(0, 0) },
		stop:     make(chan struct{}),
	}
	m.killFn = m.kill
	m.SetBinary("slack", "/x/slack")
	if !m.IsPlugin("slack") {
		t.Fatal("SetBinary should register the key")
	}
	m.RemoveBinary("slack")
	if m.IsPlugin("slack") {
		t.Fatal("RemoveBinary should drop the key")
	}
}

func TestNewManagerCreatesSocketDir(t *testing.T) {
	dir := t.TempDir() + "/run"
	t.Setenv("WICK_PLUGIN_SOCKET_DIR", dir)
	m := NewManager(map[string]string{}, time.Minute)
	defer m.KillAll()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("socket dir not created: %v", err)
	}
	// Unix permission bits aren't honored on Windows (MkdirAll's mode is
	// effectively ignored — the dir always reports 0777), so only assert the
	// 0700 mode on platforms where it's meaningful.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir perm = %o, want 0700", info.Mode().Perm())
	}
	if m.socketDir != dir {
		t.Fatalf("manager socketDir = %q, want %q", m.socketDir, dir)
	}
}
