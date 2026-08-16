package agents

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// The report must state plainly when it cannot protect anything. An
// operator reading a normal-looking dashboard on a machine with no scope
// support would otherwise believe they are guarded when they are not.
func TestBuildMemoryReport_ReportsUnavailability(t *testing.T) {
	rep := buildMemoryReport()

	if !rep.ScopesAvailable && rep.Notice == "" {
		t.Fatal("scopes unavailable but no notice explaining it")
	}
	if rep.ScopesAvailable && rep.Notice != "" {
		t.Fatalf("scopes available but a notice was set: %q", rep.Notice)
	}
}

// A fresh install has no config rows at all. The report must then say the
// guard is off — not "" — or the UI renders a blank where a state belongs.
func TestBuildMemoryReport_DefaultsWhenUnconfigured(t *testing.T) {
	rep := buildMemoryReport()

	if rep.Mode != agentconfig.MemGuardOff {
		t.Fatalf("mode = %q, want %q on an unconfigured install", rep.Mode, agentconfig.MemGuardOff)
	}
	if rep.Method != agentconfig.MethodAuto {
		t.Fatalf("method = %q, want %q on an unconfigured install", rep.Method, agentconfig.MethodAuto)
	}
}

// An empty agent list means two different things — nothing running, or
// nothing readable — and the operator needs to tell them apart.
func TestBuildMemoryReport_DistinguishesUnreadableFromEmpty(t *testing.T) {
	rep := buildMemoryReport()

	if !rep.ProcessesReadable && len(rep.Agents) > 0 {
		t.Fatal("agents reported despite processes being unreadable")
	}
}
