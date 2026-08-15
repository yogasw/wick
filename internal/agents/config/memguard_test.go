package config

import (
	"testing"

	"github.com/yogasw/wick/pkg/entity"
)

// The server's loaders read these EXACT keys via configsSvc.GetOwned
// (server.go: memory_guard_mode, agent_memory_max_mb, ...). The keys are
// derived from field names by StructToConfigs's snake_case rules, so a
// renamed field — or a change in how acronyms like MB are split — would
// make every loader silently read "" and the guard silently switch off.
// This pins the derived keys to what the loaders actually ask for.
func TestMemoryGuardConfigKeys_MatchLoaders(t *testing.T) {
	got := map[string]bool{}
	for _, c := range entity.StructToConfigs(DefaultGeneralConfig()) {
		got[c.Key] = true
	}
	for _, want := range []string{
		"memory_guard_mode",
		"memory_guard_method",
		"agent_memory_max_mb",
		"agents_total_memory_mb",
		"tool_memory_max_mb",
		"min_free_memory_mb",
		"protect_wick_from_oom",
		"agents_cpu_weight",
		"agents_cpu_quota_pct",
		"agents_tasks_max",
		"agents_io_weight",
	} {
		if !got[want] {
			t.Fatalf("key %q not derived by StructToConfigs — the loader reading it gets an empty value", want)
		}
	}
}

// Per-instance overrides global, and — unlike MaxConcurrent — it may
// EXCEED it. That asymmetry is the whole point: the instance driving a
// browser gets more without making every other agent fatter. A min() here
// would force the operator to raise the global ceiling instead.
func TestResolveAgentLimitMB(t *testing.T) {
	cases := []struct {
		name             string
		instance, global int
		want             int
	}{
		{"zero instance inherits global", 0, 2048, 2048},
		{"instance lowers", 1024, 2048, 1024},
		{"instance may exceed global", 4096, 2048, 4096},
		{"both zero is unlimited", 0, 0, 0},
		{"negative instance treated as unset", -1, 2048, 2048},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveAgentLimitMB(c.instance, c.global); got != c.want {
				t.Fatalf("ResolveAgentLimitMB(%d, %d) = %d, want %d",
					c.instance, c.global, got, c.want)
			}
		})
	}
}

// A 3 GB box and a 32 GB box must not get the same defaults, and the
// small box must not be handed a budget it does not have.
func TestDeriveMemoryDefaults_ScalesWithRAM(t *testing.T) {
	const gb = uint64(1024 * 1024 * 1024)

	small := DeriveMemoryDefaults(3*gb, 1)
	if small.AgentsTotalMB != 2072 {
		t.Fatalf("3GB aggregate = %d, want 2072 (3072 - 400 - 600)", small.AgentsTotalMB)
	}
	if small.AgentMaxMB != small.AgentsTotalMB {
		t.Fatalf("at MaxConcurrent=1 the per-agent limit should equal the budget, got %d vs %d",
			small.AgentMaxMB, small.AgentsTotalMB)
	}

	big := DeriveMemoryDefaults(32*gb, 4)
	if big.AgentMaxMB <= small.AgentMaxMB {
		t.Fatalf("32GB per-agent (%d) not above 3GB per-agent (%d)", big.AgentMaxMB, small.AgentMaxMB)
	}
	if big.AgentMaxMB*4 > big.AgentsTotalMB {
		t.Fatalf("per-agent %d x 4 exceeds the aggregate %d", big.AgentMaxMB, big.AgentsTotalMB)
	}
}

// A machine too small to host an agent must yield a floor, never a
// negative or zero budget that would read as "unlimited".
func TestDeriveMemoryDefaults_TinyMachineFloors(t *testing.T) {
	got := DeriveMemoryDefaults(512*1024*1024, 1)
	if got.AgentMaxMB <= 0 || got.AgentsTotalMB <= 0 {
		t.Fatalf("tiny machine produced non-positive limits: %+v", got)
	}
	if got.MinFreeMB <= 0 {
		t.Fatalf("tiny machine produced MinFreeMB=%d", got.MinFreeMB)
	}
	if got.ToolMaxMB <= 0 {
		t.Fatalf("tiny machine produced ToolMaxMB=%d", got.ToolMaxMB)
	}
}

// Tool subprocesses get a smaller ceiling than agents: a grep does not
// need an agent's budget, and a tight limit is what makes the failure
// recoverable rather than fatal.
func TestDeriveMemoryDefaults_ToolLimitIsSmaller(t *testing.T) {
	got := DeriveMemoryDefaults(8*1024*1024*1024, 2)
	if got.ToolMaxMB >= got.AgentMaxMB {
		t.Fatalf("tool limit %d not below agent limit %d", got.ToolMaxMB, got.AgentMaxMB)
	}
	if got.ToolMaxMB > toolCapMB {
		t.Fatalf("tool limit %d exceeds the %d MB cap", got.ToolMaxMB, toolCapMB)
	}
}

// A zero or negative concurrency must not divide by zero or hand one
// agent a fraction of a megabyte.
func TestDeriveMemoryDefaults_ZeroConcurrency(t *testing.T) {
	got := DeriveMemoryDefaults(8*1024*1024*1024, 0)
	if got.AgentMaxMB != got.AgentsTotalMB {
		t.Fatalf("zero concurrency should behave as 1: per-agent %d vs budget %d",
			got.AgentMaxMB, got.AgentsTotalMB)
	}
}
