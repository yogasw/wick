package config

// memguard.go holds the memory-guard vocabulary and the arithmetic behind
// its defaults. The mechanism lives in internal/agents/provider/memscope;
// this file only decides the numbers.

// Guard modes. off is genuinely nothing — no slice unit, no oom_score_adj,
// no argv wrapping — so an install that never opts in behaves exactly as
// it did before the feature existed.
const (
	MemGuardOff     = "off"
	MemGuardMeasure = "measure"
	MemGuardEnforce = "enforce"
)

// Guard methods: who wraps the spawn.
//
// wrapper means something outside wick already does it (a symlink wrapper
// on the agent binary), so wick measures but does not wrap. Double-wrapping
// is harmless — the kernel enforces every ceiling in the hierarchy and the
// tightest wins — so this is about who owns the setting, not safety.
const (
	MethodAuto    = "auto"
	MethodScope   = "scope"
	MethodWrapper = "wrapper"
)

// Reserves carved out of RAM before agents get a budget.
const (
	reserveOSMB   = 400
	reserveWickMB = 600
	// floorMB keeps a tiny machine from deriving a zero budget, which
	// would read as "unlimited" and invert the whole point.
	floorMB = 256
	// toolCapMB bounds a tool subprocess. A grep does not need an agent's
	// budget, and a tight ceiling is what makes its failure recoverable.
	toolCapMB = 512
	// toolFloorMB keeps the tool ceiling usable on a small machine.
	toolFloorMB = 64
)

// MemoryDefaults are the derived starting values for one machine.
type MemoryDefaults struct {
	AgentsTotalMB int
	AgentMaxMB    int
	ToolMaxMB     int
	MinFreeMB     int
}

// DeriveMemoryDefaults scales the defaults to the machine. A 3 GB box and
// a 32 GB box must not start from the same numbers.
func DeriveMemoryDefaults(totalRAMBytes uint64, maxConcurrent int) MemoryDefaults {
	totalMB := int(totalRAMBytes / (1024 * 1024))

	budget := totalMB - reserveOSMB - reserveWickMB
	if budget < floorMB {
		budget = floorMB
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}

	perAgent := budget / maxConcurrent
	if perAgent < floorMB {
		perAgent = floorMB
	}

	tool := perAgent / 4
	if tool > toolCapMB {
		tool = toolCapMB
	}
	if tool < toolFloorMB {
		tool = toolFloorMB
	}

	minFree := totalMB / 8
	if minFree < floorMB {
		minFree = floorMB
	}

	return MemoryDefaults{
		AgentsTotalMB: budget,
		AgentMaxMB:    perAgent,
		ToolMaxMB:     tool,
		MinFreeMB:     minFree,
	}
}

// ResolveAgentLimitMB picks the ceiling for one spawn.
//
// Deliberately unlike MaxConcurrent, which resolves as min(provider,
// global) because slots are a shared pool. A memory ceiling is per
// process, so a per-instance value MAY exceed the global default — that
// is precisely how one heavy instance is accommodated without letting
// every other agent grow. A min() here would force the operator to raise
// the global ceiling instead, making every agent fatter to serve one.
func ResolveAgentLimitMB(instanceMB, globalMB int) int {
	if instanceMB > 0 {
		return instanceMB
	}
	return globalMB
}
