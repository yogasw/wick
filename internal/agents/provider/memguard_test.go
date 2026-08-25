package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// withScopesAvailable forces the availability probe for a test, so both
// branches are exercised on a machine with no systemd user session.
// "Available" here means the systemd backend specifically — existing
// tests that only ever cared about available-or-not keep asserting
// against the systemd argv shape (bin == "systemd-run") unchanged.
// withBackend below drives the cgroupfs branch explicitly.
func withScopesAvailable(t *testing.T, available bool) {
	t.Helper()
	backend := memscope.BackendNone
	if available {
		backend = memscope.BackendSystemd
	}
	withBackend(t, backend)
}

// withBackend forces DetectBackend's result for a test.
func withBackend(t *testing.T, b memscope.Backend) {
	t.Helper()
	prev := memscopeBackend
	memscopeBackend = func() memscope.Backend { return b }
	t.Cleanup(func() { memscopeBackend = prev })
}

// withSelfExecutable forces selfExecutable's result for a test — the
// cgroupfs backend's only extra input beyond what withBackend covers.
func withSelfExecutable(t *testing.T, path string, err error) {
	t.Helper()
	prev := selfExecutable
	selfExecutable = func() (string, error) { return path, err }
	t.Cleanup(func() { selfExecutable = prev })
}

// Mode off must be indistinguishable from the feature not existing: the
// argv reaches exec untouched and no scope name is claimed.
func TestMemGuard_OffDoesNotWrap(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardOff, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

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
	if _, _, ok := g.ClassifyExit("some-unit", 512, 0); ok {
		t.Fatal("nil guard classified an OOM")
	}
}

// OnSpawn off means something outside wick covers these agents — a shim
// on the path. Wick must not wrap here, not because double-wrapping is
// unsafe (it is not), but because this is the operator saying where the
// limit comes from.
func TestMemGuard_OnPathOnlyDefersToTheShim(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnPath: true}, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary when only OnPath is set", bin)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty when only OnPath is set", unit)
	}
}

// When the mechanism is missing, wick must run the agent unguarded rather
// than refuse to spawn: a degraded agent beats no agent.
func TestMemGuard_UnavailableScopesDoNotBlockSpawn(t *testing.T) {
	withScopesAvailable(t, false)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)
	if bin != "/usr/bin/claude" || unit != "" {
		t.Fatalf("bin=%q unit=%q, want an unwrapped spawn when scopes are unavailable", bin, unit)
	}
}

// Enforce sets the ceiling it was given.
func TestMemGuard_EnforceAppliesLimit(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1536}

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
	g := &MemGuard{Mode: config.MemGuardMeasure, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

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
		Mode: config.MemGuardMeasure, Scopes: config.GuardScopes{OnSpawn: true},
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
		Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true},
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
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

	if _, _, ok := g.ClassifyExit("", 1024, 0); ok {
		t.Fatal("classified an OOM with no scope to read")
	}
	if _, _, ok := g.ClassifyExit("claude-agent-does-not-exist", 1024, 0); ok {
		t.Fatal("classified an OOM from a scope that does not exist")
	}
}

// classifyStats must tell a limit kill from a global one. oom_kill counts
// kills by ANY OOM killer, so a host that ran out of memory used to be
// reported as "over its limit" even when the measured peak was under it —
// and, worse, was never retried, though the agent did nothing wrong.
func TestClassifyStats_SeparatesHostFromLimitOOM(t *testing.T) {
	// Limit kill: the cgroup raised its own OOM condition. Not retryable.
	r, d, ok := classifyStats(memscope.Stats{Known: true, OOMKills: 1, OOMEvents: 2, PeakBytes: 2 << 30}, 1024, 0)
	if !ok || r != ExitOOM {
		t.Fatalf("limit kill: reason=%v ok=%v, want ExitOOM true", r, ok)
	}
	if d == "" {
		t.Fatal("limit kill: empty detail")
	}

	// Global kill: killed by the host OOM killer without hitting its own
	// ceiling. Stays ExitError (retryable) with a detail naming the host.
	r, d, ok = classifyStats(memscope.Stats{Known: true, OOMKills: 1, OOMEvents: 0, PeakBytes: 1 << 30}, 1285, 0)
	if !ok || r != ExitError {
		t.Fatalf("global kill: reason=%v ok=%v, want ExitError true", r, ok)
	}
	if d == "" || !strings.Contains(d, "machine") {
		t.Fatalf("global kill: detail must name the machine, got %q", d)
	}
	if strings.Contains(d, "over its") {
		t.Fatalf("global kill: detail claims the limit was exceeded: %q", d)
	}

	// No kill at all: no verdict, regardless of the oom counter.
	if _, _, ok := classifyStats(memscope.Stats{Known: true, OOMEvents: 1}, 1024, 0); ok {
		t.Fatal("classified an OOM with zero kills")
	}
	if _, _, ok := classifyStats(memscope.Stats{}, 1024, 0); ok {
		t.Fatal("classified an OOM from unknown stats")
	}
}

// A kill by the aggregate slice ceiling (agents_total_memory_mb) leaves
// the scope's own oom at 0 — locally identical to a host OOM. The slice's
// oom counter, snapshotted at spawn and diffed at exit, is the
// discriminator. Still retryable (contention, not this agent's ceiling),
// but the detail must name the shared limit, not the machine.
func TestClassifyStats_AggregateSliceKill(t *testing.T) {
	r, d, ok := classifyStats(memscope.Stats{Known: true, OOMKills: 1, OOMEvents: 0, PeakBytes: 1 << 30}, 1024, 2)
	if !ok || r != ExitError {
		t.Fatalf("aggregate kill: reason=%v ok=%v, want ExitError true", r, ok)
	}
	if !strings.Contains(d, "combined") {
		t.Fatalf("aggregate kill: detail must name the combined limit, got %q", d)
	}
	if strings.Contains(d, "machine ran out") {
		t.Fatalf("aggregate kill: detail blames the machine: %q", d)
	}

	// Own-limit kill wins over a concurrent slice event: the scope's own
	// oom counter is the more specific evidence.
	r, _, ok = classifyStats(memscope.Stats{Known: true, OOMKills: 1, OOMEvents: 1}, 1024, 2)
	if !ok || r != ExitOOM {
		t.Fatalf("own limit with slice event: reason=%v ok=%v, want ExitOOM true", r, ok)
	}
}

// A cgroupfs spawn that cannot resolve wick's own binary path has no
// wrapper to re-exec through. It must degrade to unguarded, matching how
// an EnsureSlice failure degrades on the systemd path — never refuse the
// spawn outright.
func TestMemGuard_CgroupFSBackendDegradesWhenSelfPathUnresolvable(t *testing.T) {
	withBackend(t, memscope.BackendCgroupFS)
	withSelfExecutable(t, "", errors.New("os.Executable: not supported"))
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 512}

	bin, argv, unit := g.Wrap("/usr/bin/claude", []string{"--foo"}, "claude", 5)
	if bin != "/usr/bin/claude" {
		t.Fatalf("bin = %q, want the original binary when self-path resolution fails", bin)
	}
	if len(argv) != 1 || argv[0] != "--foo" {
		t.Fatalf("argv = %v, want it untouched", argv)
	}
	if unit != "" {
		t.Fatalf("unit = %q, want empty — this spawn was not wrapped", unit)
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

// withRemoveCgroupScope captures scope-removal calls instead of touching
// the host hierarchy.
func withRemoveCgroupScope(t *testing.T) *[]string {
	t.Helper()
	var seen []string
	prev := removeCgroupScope
	removeCgroupScope = func(unit string) error {
		seen = append(seen, unit)
		return nil
	}
	t.Cleanup(func() { removeCgroupScope = prev })
	return &seen
}

// A cgroupfs scope is left behind by every spawn unless wick removes it:
// there is no --collect equivalent, and ScopeUnitName's sequence means
// names never repeat, so they accumulate for as long as the host lives.
func TestMemGuard_ReleaseScopeRemovesACgroupFSScope(t *testing.T) {
	withBackend(t, memscope.BackendCgroupFS)
	seen := withRemoveCgroupScope(t)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

	g.ReleaseScope("claude-agent-1")

	if len(*seen) != 1 || (*seen)[0] != "claude-agent-1" {
		t.Fatalf("removed %v, want exactly claude-agent-1", *seen)
	}
}

// systemd-run is passed --collect, which reaps the scope itself. Removing
// it here would be a second party deleting a directory systemd owns.
func TestMemGuard_ReleaseScopeLeavesSystemdScopesToSystemd(t *testing.T) {
	withBackend(t, memscope.BackendSystemd)
	seen := withRemoveCgroupScope(t)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 1024}

	g.ReleaseScope("claude-agent-1")

	if len(*seen) != 0 {
		t.Fatalf("removed %v on the systemd backend, which reaps its own scopes", *seen)
	}
}

// An unwrapped spawn has no scope, and a nil guard has no state at all.
// The exit path calls this unconditionally, so both must be silent.
func TestMemGuard_ReleaseScopeIgnoresUnwrappedSpawns(t *testing.T) {
	withBackend(t, memscope.BackendCgroupFS)
	seen := withRemoveCgroupScope(t)

	(&MemGuard{Mode: config.MemGuardEnforce}).ReleaseScope("")
	var nilGuard *MemGuard
	nilGuard.ReleaseScope("claude-agent-1")

	if len(*seen) != 0 {
		t.Fatalf("removed %v for a spawn that was never wrapped", *seen)
	}
}

// Both scopes at once is the point of splitting them, not a conflict.
// The shim reaches callers wick cannot see; wick still covers its own
// agents when the shim's link is replaced by a package update. The
// kernel applies every ceiling and the tightest wins, so wick keeps
// wrapping here rather than deferring.
func TestMemGuard_BothScopesStillWrapsOnSpawn(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{
		Mode:         config.MemGuardEnforce,
		Scopes:       config.GuardScopes{OnSpawn: true, OnPath: true},
		AgentLimitMB: 1024,
	}

	_, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)

	if unit == "" {
		t.Fatal("wick stopped wrapping because a shim is also installed; the two are additive, not exclusive")
	}
}

// No scope at all means the mode is set but nothing is asked for. That
// must behave as off rather than silently picking one.
func TestMemGuard_NoScopeWrapsNothing(t *testing.T) {
	withScopesAvailable(t, true)
	g := &MemGuard{Mode: config.MemGuardEnforce, AgentLimitMB: 1024}

	bin, _, unit := g.Wrap("/usr/bin/claude", nil, "claude", 1)

	if unit != "" || bin != "/usr/bin/claude" {
		t.Fatalf("wrapped with no scope selected: bin=%q unit=%q", bin, unit)
	}
}
