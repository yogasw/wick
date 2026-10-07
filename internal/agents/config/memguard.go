package config

// memguard.go holds the memory-guard vocabulary and the arithmetic behind
// its defaults. The mechanism lives in internal/agents/provider/memscope;
// this file only decides the numbers.

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultCPUQuotaPct is agents_cpu_quota_pct when it was never set: 80% of
// the whole machine, a safe cap whatever the core count.
const DefaultCPUQuotaPct = 80

// CPUQuotaMachinePct reads agents_cpu_quota_pct as a share of the whole
// machine, 0..100 (0 = no cap). Empty or unreadable is DefaultCPUQuotaPct.
// A value above 100 was saved when the setting meant percent of ONE core,
// so it is read that way: divided by cores (140 on 2 cores = 70).
func CPUQuotaMachinePct(raw string, cores int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	switch {
	case err != nil:
		return DefaultCPUQuotaPct
	case n <= 0:
		return 0
	case n > 100:
		return min(100, n/max(1, cores))
	}
	return n
}

// CPUQuotaCorePct turns a machine share into systemd's CPUQuota, which is
// percent of one core: 80% of a 2-core machine = 160.
func CPUQuotaCorePct(machinePct, cores int) int {
	return machinePct * max(1, cores)
}

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
//
// Only auto and wrapper are offered in the dropdown, because only those two
// behave differently. MethodScope is accepted, never produced.
const (
	MethodAuto = "auto"
	// MethodScope is a legacy value, kept only so a config that already
	// stores it keeps loading. It was documented as "wick always applies
	// it", but nothing ever distinguished it from auto: MemGuard.wraps
	// only ever tests for MethodWrapper, so scope took the identical
	// branch. There is no honest stronger meaning available either —
	// "always wrap" cannot be delivered on a machine with no cgroups, and
	// forcing it would refuse spawns where every other failure in this
	// subsystem degrades instead. Do not put it back in the dropdown, and
	// do not add a branch for it.
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

// headroomPct is added to a measured peak to get a suggested limit.
//
// A ceiling set exactly at the observed peak kills the next run that does
// slightly more work — the failure that makes operators distrust the
// guard and switch it off.
const headroomPct = 30

// SuggestLimitMB turns a measured peak into a suggested ceiling in MB.
//
// The arithmetic is done in BYTES and rounded up at the end. Converting
// to MB first and scaling afterwards loses the headroom entirely at small
// sizes: 1.5 MB truncates to 1 MB, and 1*130/100 is 1 again — a
// "suggestion" identical to the peak it was supposed to leave room above.
// Rounding up also guarantees the result is never below the peak itself.
//
// Returns 0 for a non-positive peak so callers can tell "no measurement"
// from "measured, and small".
func SuggestLimitMB(peakBytes uint64) int {
	if peakBytes == 0 {
		return 0
	}
	withHeadroom := peakBytes * (100 + headroomPct) / 100
	const mb = 1024 * 1024
	// Round up: a limit below the measured peak is never a useful answer.
	return int((withHeadroom + mb - 1) / mb)
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

// ValidateMemoryBudget refuses a combined agent ceiling that, with wick's
// own reserve and the free-memory floor, adds up to more than the
// machine has. Such a ceiling never binds: the host runs out first, and
// on a small host without swap that is a hang, not an OOM kill.
// ramMB <= 0 (unknown machine size) or a zero ceiling accepts.
func ValidateMemoryBudget(totalAgentsMB, minFreeMB, ramMB int) error {
	if ramMB <= 0 || totalAgentsMB <= 0 {
		return nil
	}
	if minFreeMB < 0 {
		minFreeMB = 0
	}
	need := totalAgentsMB + reserveWickMB + minFreeMB
	if need <= ramMB {
		return nil
	}
	max := ramMB - reserveWickMB - minFreeMB
	if max < 0 {
		max = 0
	}
	return fmt.Errorf("agents_total_memory_mb %d + wick reserve %d + min_free_memory_mb %d = %d MB, more than this machine's %d MB; "+
		"the ceiling could never be reached before the machine runs out — use at most %d MB (or 0 for none)",
		totalAgentsMB, reserveWickMB, minFreeMB, need, ramMB, max)
}
