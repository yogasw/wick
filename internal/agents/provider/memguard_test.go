package provider

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// withScopesAvailable forces the availability probe for a test, so both
// branches are exercised on a machine with no systemd user session.
func withScopesAvailable(t *testing.T, available bool) {
	t.Helper()
	prev := memscopeAvailable
	memscopeAvailable = func() bool { return available }
	t.Cleanup(func() { memscopeAvailable = prev })
}

// Mode off must be indistinguishable from the feature not existing: the
// argv reaches exec untouched and no scope name is claimed.
func TestMemGuard_OffDoesNotWrap(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardOff, Method: config.MethodScope, AgentLimitMB: 1024}

	bin, argv, unit := g.Wrap("/usr/bin/claude", []string{"--foo"}, "claude", 1)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary", bin)
	}
	if len(argv) != 1 || argv[0] != "--foo" {
		t.Fatalf("argv = %v, want it untouched", argv)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty when unwrapped", unit)
	}
}

// A nil guard is the "feature absent" path every existing caller takes.
// It must never panic and never wrap.
func TestMemGuard_NilIsInert(t *testing.T) {
	var g *MemGuard

	bin, argv, unit := g.Wrap("/usr/bin/claude", []string{"--foo"}, "claude", 1)
	if bin != "/usr/bin/claude" || len(argv) != 1 || unit != "" {
		t.Fatalf("nil guard altered the spawn: bin=%q argv=%v unit=%q", bin, argv, unit)
	}
	g.BiasChild(123) // must not panic
	if _, _, ok := g.ClassifyExit("some-unit", 512); ok {
		t.Fatal("nil guard classified an OOM")
	}
}

// Method wrapper means something outside wick already wraps. Wick must
// not wrap again by itself here — not because double-wrapping is unsafe
// (it is not), but because this is the operator saying who owns it.
func TestMemGuard_WrapperMethodDefersToExternal(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodWrapper, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary under method=wrapper", bin)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty under method=wrapper", unit)
	}
}

// When the mechanism is missing, wick must run the agent unguarded rather
// than refuse to spawn: a degraded agent beats no agent.
func TestMemGuard_UnavailableScopesDoNotBlockSpawn(t *testing.T) {
	withScopesAvailable(t, false)
	g := &MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodScope, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)
	if bin != "/usr/bin/claude" || unit != "" {
		t.Fatalf("bin=%q unit=%q, want an unwrapped spawn when scopes are unavailable", bin, unit)
	}
}

// Enforce sets the ceiling it was given.
func TestMemGuard_EnforceAppliesLimit(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodScope, AgentLimitMB: 1536}

	bin, argv, unit := g.Wrap("/usr/bin/claude", nil, "claude", 2)
	if bin != "systemd-run" {
		t.Fatalf("bin = %q, want systemd-run", bin)
	}
	if unit == "" {
		t.Fatal("enforce mode created no scope")
	}
	if !strings.Contains(strings.Join(argv, " "), "MemoryMax=1536M") {
		t.Fatalf("argv %v does not carry the limit", argv)
	}
}

// Measure mode creates the scope so a peak can be read, but sets no
// ceiling — turning measurement on must never change what dies.
func TestMemGuard_MeasureCreatesScopeWithoutLimit(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardMeasure, Method: config.MethodScope, AgentLimitMB: 1024}

	_, argv, unit := g.Wrap("/usr/bin/claude", nil, "claude", 3)
	if unit == "" {
		t.Fatal("measure mode did not create a scope; peaks would be unreadable")
	}
	if strings.Contains(strings.Join(argv, " "), "MemoryMax=") {
		t.Fatalf("measure mode set a ceiling: %v", argv)
	}
}

// Measure mode's entire promise is "record numbers, change nothing" — and
// the aggregate ceiling, CPU weight, and the rest all change behaviour.
// An earlier revision wrote the aggregate MemoryMax in measure mode too,
// which could kill agents collectively while the operator believed
// nothing was enforced. This pins the fix.
func TestMemGuard_MeasureWritesNoSliceLimits(t *testing.T) {
	g := &MemGuard{
		Mode: config.MemGuardMeasure, Method: config.MethodScope,
		AgentLimitMB: 1024, AggregateMB: 2048,
		CPUWeight: 50, CPUQuotaPct: 150, TasksMax: 512, IOWeight: 80,
	}
	if got := g.sliceLimits(); got != (memscope.SliceLimits{}) {
		t.Fatalf("measure mode produced slice limits %+v, want none", got)
	}
}

// Enforce carries every configured control onto the slice.
func TestMemGuard_EnforceCarriesSliceLimits(t *testing.T) {
	g := &MemGuard{
		Mode: config.MemGuardEnforce, Method: config.MethodScope,
		AggregateMB: 2048, CPUWeight: 50, CPUQuotaPct: 150, TasksMax: 512, IOWeight: 80,
	}
	want := memscope.SliceLimits{
		AggregateMB: 2048, CPUWeight: 50, CPUQuotaPct: 150, TasksMax: 512, IOWeight: 80,
	}
	if got := g.sliceLimits(); got != want {
		t.Fatalf("enforce slice limits = %+v, want %+v", got, want)
	}
}

// An OOM verdict requires evidence. Without a readable scope the exit
// must fall through to the ordinary classification, never guess.
func TestMemGuard_ClassifyExitWithoutEvidence(t *testing.T) {
	g := &MemGuard{Mode: config.MemGuardEnforce, Method: config.MethodScope, AgentLimitMB: 1024}

	if _, _, ok := g.ClassifyExit("", 1024); ok {
		t.Fatal("classified an OOM with no scope to read")
	}
	if _, _, ok := g.ClassifyExit("claude-agent-does-not-exist", 1024); ok {
		t.Fatal("classified an OOM from a scope that does not exist")
	}
}

// BiasChild only acts in enforce, and only when asked to protect wick —
// measure mode must leave the machine exactly as it found it.
func TestMemGuard_BiasChildRespectsMode(t *testing.T) {
	for _, c := range []struct {
		name string
		g    *MemGuard
	}{
		{"measure does not bias", &MemGuard{Mode: config.MemGuardMeasure, ProtectWick: true}},
		{"off does not bias", &MemGuard{Mode: config.MemGuardOff, ProtectWick: true}},
		{"protect disabled does not bias", &MemGuard{Mode: config.MemGuardEnforce, ProtectWick: false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.g.BiasChild(0) // a zero pid would be a real error if it got through
		})
	}
}
