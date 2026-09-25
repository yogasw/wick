package agentmemory

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// The autostart lock is a DERIVED state, not a stored switch (PLAN §10.3):
// while a provider instance has Agent Memory on, its backend's daemon must
// start at boot, and the operator cannot switch that off. These tests pin both
// halves of the rule — free when nothing uses the backend, forced on (with the
// stored value left alone) when something does.

// withInstances makes UsedBy read the given instances for one test.
func withInstances(t *testing.T, list []provider.Instance, err error) {
	t.Helper()
	prev := loadInstances
	loadInstances = func() ([]provider.Instance, error) { return list, err }
	t.Cleanup(func() { loadInstances = prev })
}

func TestAutostartFreeWhenNoInstanceUsesTheBackend(t *testing.T) {
	withInstances(t, []provider.Instance{
		// Memory off entirely.
		{Type: provider.TypeClaude, Name: "claude"},
		// Memory on, but pointed at a different backend.
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true, AgentMemoryProvider: "other-backend"},
	}, nil)

	lock := AutostartLockFor("lock-mem")
	if lock.Locked || len(lock.UsedBy) != 0 || lock.Reason != "" {
		t.Fatalf("no instance uses lock-mem, so autostart must stay free: %+v", lock)
	}
	// With the lock off, the stored value is the whole answer.
	if (Settings{Autostart: false, AutostartLocked: lock.Locked}).EffectiveAutostart() {
		t.Fatal("autostart off + no lock should not start the daemon")
	}
	if !(Settings{Autostart: true, AutostartLocked: lock.Locked}).EffectiveAutostart() {
		t.Fatal("autostart on should still start the daemon without a lock")
	}
}

func TestAutostartLockedWhileInstancesUseTheBackend(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "enginer", UseAgentMemory: true, AgentMemoryProvider: "lock-mem", AgentMemoryCapture: true},
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true, AgentMemoryProvider: "lock-mem"},
		{Type: provider.TypeClaude, Name: "off", UseAgentMemory: false, AgentMemoryProvider: "lock-mem"},
	}, nil)

	lock := AutostartLockFor("lock-mem")
	if !lock.Locked {
		t.Fatal("an instance uses lock-mem, so autostart must be locked on")
	}
	if len(lock.UsedBy) != 2 {
		t.Fatalf("only the instances with the toggle ON hold the lock, got %+v", lock.UsedBy)
	}
	// The reason is what the FE shows next to the disabled control, so it has
	// to name who is holding it.
	if !strings.Contains(lock.Reason, "claude/enginer") || !strings.Contains(lock.Reason, "codex") {
		t.Fatalf("reason should name the instances holding the lock: %q", lock.Reason)
	}
	// The lock beats a stored "off" — that is the whole point of deriving it
	// rather than storing a second switch that can disagree.
	if !(Settings{Autostart: false, AutostartLocked: lock.Locked}).EffectiveAutostart() {
		t.Fatal("a locked backend must start at boot even with autostart stored off")
	}
}

// A capture-only distinction must not leak into the lock: recall-only still
// needs the daemon up.
func TestAutostartLockIgnoresCapture(t *testing.T) {
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "claude", UseAgentMemory: true, AgentMemoryProvider: "lock-mem", AgentMemoryCapture: false},
	}, nil)

	lock := AutostartLockFor("lock-mem")
	if !lock.Locked {
		t.Fatal("a recall-only instance still depends on the daemon")
	}
	if lock.UsedBy[0].Capture {
		t.Fatalf("capture state should be reported as-is: %+v", lock.UsedBy[0])
	}
	// The per-type default instance is named after its type; the UI shouldn't
	// say "claude/claude".
	if got := lock.UsedBy[0].Label(); got != "claude" {
		t.Fatalf("default instance label = %q, want %q", got, "claude")
	}
	if !strings.Contains(lock.Reason, "uses Agent Memory") {
		t.Fatalf("single holder should read in the singular: %q", lock.Reason)
	}
}

// A backend id an instance left empty resolves to the default backend, so the
// lock follows the same resolution the spawn does.
func TestAutostartLockFollowsDefaultBackendID(t *testing.T) {
	withInstances(t, []provider.Instance{
		// No AgentMemoryProvider — the spawn would resolve it to the
		// default backend, and so must the lock.
		{Type: provider.TypeClaude, Name: "claude", UseAgentMemory: true},
	}, nil)

	if !AutostartLockFor(backendID("")).Locked {
		t.Fatal("an instance that names no backend still locks the default one")
	}
	if AutostartLockFor("a-backend-nobody-selected").Locked {
		t.Fatal("the lock must not spill onto a backend nothing points at")
	}
}

// A failed provider load must not pin autostart on from a read wick could not
// verify — the operator keeps control.
func TestAutostartUnlockedWhenInstancesCannotBeRead(t *testing.T) {
	withInstances(t, nil, errors.New("config unreadable"))

	lock := AutostartLockFor("lock-mem")
	if lock.Locked || lock.Reason != "" {
		t.Fatalf("a failed load should unlock, not pin: %+v", lock)
	}
}

// settingsFor is the only read of the settings, so the derived lock has to
// ride along with the stored values — and never be written back.
func TestSettingsForAppliesTheDerivedLock(t *testing.T) {
	withStore(t, &fakeStore{enabled: true, admin: true, set: Settings{Autostart: false, Port: 49374}})
	withInstances(t, []provider.Instance{
		{Type: provider.TypeCodex, Name: "codex", UseAgentMemory: true, AgentMemoryProvider: "lock-mem"},
	}, nil)

	set, lock := settingsWithLock("lock-mem")
	if !set.AutostartLocked || !set.EffectiveAutostart() {
		t.Fatalf("settings should carry the lock: %+v", set)
	}
	if set.Autostart {
		t.Fatal("the stored autostart value must stay as the operator left it")
	}
	if !lock.Locked || len(lock.UsedBy) != 1 {
		t.Fatalf("lock not reported alongside the settings: %+v", lock)
	}
}

// AnyAutostartEnabled is what server.go gates the boot step on, so the lock
// has to reach it too.
func TestAnyAutostartEnabledSeesTheLock(t *testing.T) {
	registerTestBackend(t, "lock-mem-boot", 41910)
	withStore(t, &fakeStore{enabled: true, admin: true, set: Settings{Autostart: false}})
	withInstances(t, nil, nil)

	if AnyAutostartEnabled() {
		t.Fatal("nothing stored on and nothing using it — no boot step")
	}
	withInstances(t, []provider.Instance{
		{Type: provider.TypeClaude, Name: "claude", UseAgentMemory: true, AgentMemoryProvider: "lock-mem-boot"},
	}, nil)
	if !AnyAutostartEnabled() {
		t.Fatal("an instance using a backend must put its daemon in the boot path")
	}
}
