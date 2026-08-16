package memscope

import (
	"strings"
	"testing"
)

// The two properties that separate a clean kill from an outage. A
// MemoryHigh in a scope throttles instead of killing and produced a
// 116-minute production stall; swap turns a fast death into thrashing.
// Assert them explicitly rather than trusting review.
func TestWrapArgv_PinsHighAndSwap(t *testing.T) {
	_, argv := WrapArgv("/usr/bin/claude", []string{"--foo"}, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 1536,
	})
	joined := strings.Join(argv, " ")

	for _, want := range []string{
		"-p", "MemoryHigh=infinity", "MemorySwapMax=0", "MemoryMax=1536M",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q missing %q", joined, want)
		}
	}
}

// The wrapper must hand off to the real binary with its arguments intact
// and clearly separated by --, or the agent gets the wrong argv.
func TestWrapArgv_PassesThroughBinaryAndArgs(t *testing.T) {
	bin, argv := WrapArgv("/usr/bin/claude", []string{"--output-format", "stream-json"}, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 1024,
	})
	if bin != "systemd-run" {
		t.Fatalf("bin = %q, want systemd-run", bin)
	}
	sep := -1
	for i, a := range argv {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		t.Fatal("argv has no -- separator before the real command")
	}
	rest := strings.Join(argv[sep+1:], " ")
	if rest != "/usr/bin/claude --output-format stream-json" {
		t.Fatalf("command after -- = %q", rest)
	}
}

// A zero limit means "no per-scope ceiling" (measure mode): the scope is
// still created so memory.peak is readable, but MemoryMax is not set.
func TestWrapArgv_ZeroLimitOmitsMemoryMax(t *testing.T) {
	_, argv := WrapArgv("/usr/bin/claude", nil, Opts{
		Unit: "claude-agent-7", Slice: SliceName, LimitMB: 0,
	})
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "MemoryMax=") {
		t.Fatalf("argv %q sets MemoryMax despite a zero limit", joined)
	}
	if !strings.Contains(joined, "MemoryHigh=infinity") {
		t.Fatalf("argv %q dropped MemoryHigh even with no ceiling", joined)
	}
}

// An empty slice must still land in the agents slice, or a spawn escapes
// the aggregate ceiling entirely.
func TestWrapArgv_EmptySliceDefaults(t *testing.T) {
	_, argv := WrapArgv("/usr/bin/claude", nil, Opts{Unit: "claude-agent-1"})
	if !strings.Contains(strings.Join(argv, " "), "--slice="+SliceName) {
		t.Fatalf("argv %v did not default to %s", argv, SliceName)
	}
}

// The slice unit carries the aggregate ceiling and the same two pins.
func TestRenderSlice(t *testing.T) {
	got := RenderSlice(SliceLimits{AggregateMB: 2048})
	for _, want := range []string{
		"[Slice]", "MemoryMax=2048M", "MemoryHigh=infinity", "MemorySwapMax=0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("slice unit missing %q:\n%s", want, got)
		}
	}
}

// Each contention control renders when set — with systemd's exact syntax
// (CPUQuota takes a literal % sign) — and is absent when zero, so an
// operator who configured nothing gets kernel defaults, not our guesses.
func TestRenderSlice_ContentionControls(t *testing.T) {
	got := RenderSlice(SliceLimits{
		AggregateMB: 2048, CPUWeight: 50, CPUQuotaPct: 150, TasksMax: 512, IOWeight: 80,
	})
	for _, want := range []string{
		"CPUWeight=50", "CPUQuota=150%", "TasksMax=512", "IOWeight=80",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("slice unit missing %q:\n%s", want, got)
		}
	}

	bare := RenderSlice(SliceLimits{AggregateMB: 2048})
	for _, absent := range []string{"CPUWeight=", "CPUQuota=", "TasksMax=", "IOWeight="} {
		if strings.Contains(bare, absent) {
			t.Fatalf("unconfigured control %q rendered anyway:\n%s", absent, bare)
		}
	}
}

// Rendering must be stable so the idempotent installer does not rewrite
// an unchanged file (and daemon-reload on every spawn stays free).
func TestRenderSlice_Stable(t *testing.T) {
	l := SliceLimits{AggregateMB: 2048, CPUWeight: 50, TasksMax: 512}
	if RenderSlice(l) != RenderSlice(l) {
		t.Fatal("RenderSlice is not deterministic")
	}
}

// A zero SliceLimits = grouping and measurement only: the slice exists so
// memory.peak is readable, but constrains nothing.
func TestRenderSlice_ZeroIsUnlimited(t *testing.T) {
	got := RenderSlice(SliceLimits{})
	for _, absent := range []string{"MemoryMax=", "CPUWeight=", "CPUQuota=", "TasksMax=", "IOWeight="} {
		if strings.Contains(got, absent) {
			t.Fatalf("zero limits still set %q:\n%s", absent, got)
		}
	}
}

// Scope names must be unique per spawn, or systemd refuses the second one
// while the first is alive.
func TestScopeUnitName_Unique(t *testing.T) {
	if ScopeUnitName("claude", 1) == ScopeUnitName("claude", 2) {
		t.Fatal("scope unit names collide across spawns")
	}
	if !strings.HasPrefix(ScopeUnitName("codex", 3), "codex-agent-") {
		t.Fatalf("name %q does not identify the provider", ScopeUnitName("codex", 3))
	}
}

// EnsureSlice writes the unit only when the content would change, so
// calling it on every spawn costs nothing.
func TestEnsureSliceAt_IdempotentWrite(t *testing.T) {
	dir := t.TempDir()

	changed, err := ensureSliceAt(dir, SliceLimits{AggregateMB: 2048})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !changed {
		t.Fatal("first write reported no change")
	}

	changed, err = ensureSliceAt(dir, SliceLimits{AggregateMB: 2048})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Fatal("unchanged content rewrote the unit file")
	}

	changed, err = ensureSliceAt(dir, SliceLimits{AggregateMB: 4096, CPUWeight: 50})
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if !changed {
		t.Fatal("a changed limit did not rewrite the unit")
	}
}
