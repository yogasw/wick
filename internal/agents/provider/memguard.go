package provider

import (
	"sync/atomic"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/internal/agents/provider/oomscore"
)

// memguard.go is the one place that decides whether a spawn is wrapped in
// a memory-limited scope, so the three CLI spawners stay free of policy
// and the rules cannot drift apart between them.

// MemGuard is the resolved memory policy for one spawn. A nil *MemGuard
// means the guard is off; every method below is safe on a zero value.
type MemGuard struct {
	Mode         string // config.MemGuard{Off,Measure,Enforce}
	Method       string // config.Method{Auto,Scope,Wrapper}
	AgentLimitMB int    // resolved per-agent ceiling; 0 = none
	AggregateMB  int    // slice-wide ceiling; 0 = none
	ProtectWick  bool

	// Slice-wide contention controls, enforce-mode only. Memory is the
	// only control that kills; these shape how agents compete with wick
	// and each other. See memscope.SliceLimits for what each one does.
	CPUWeight   int
	CPUQuotaPct int
	TasksMax    int
	IOWeight    int
}

// sliceLimits picks what is actually written onto agents.slice for this
// guard's mode.
//
// Measure returns the zero SliceLimits on purpose: the mode's entire
// promise is "record numbers, change nothing", and every one of these —
// the aggregate ceiling included — changes behaviour. An earlier revision
// wrote the aggregate MemoryMax in measure mode too, which could kill
// agents collectively while the operator believed nothing was enforced.
func (g *MemGuard) sliceLimits() memscope.SliceLimits {
	if g == nil || g.Mode != config.MemGuardEnforce {
		return memscope.SliceLimits{}
	}
	return memscope.SliceLimits{
		AggregateMB: g.AggregateMB,
		CPUWeight:   g.CPUWeight,
		CPUQuotaPct: g.CPUQuotaPct,
		TasksMax:    g.TasksMax,
		IOWeight:    g.IOWeight,
	}
}

// memscopeAvailable is a seam so tests can drive both branches without a
// systemd user session.
var memscopeAvailable = memscope.Available

// spawnSeq names scopes uniquely.
//
// Process-wide rather than per-agent: systemd refuses a duplicate unit
// name while the first is alive, and two different agents spawning their
// first process would otherwise both ask for "claude-agent-1".
var spawnSeq atomic.Int64

// nextSpawnSeq yields the next scope suffix.
func nextSpawnSeq() int { return int(spawnSeq.Add(1)) }

// wraps reports whether wick itself should wrap this spawn.
//
// Under method=wrapper the operator has said an external wrapper owns
// this. Wick honours that and only measures. Double-wrapping would be
// harmless — the kernel applies every ceiling in the hierarchy and the
// tightest wins — so this is about who owns the setting, not safety.
func (g *MemGuard) wraps() bool {
	if g == nil || g.Mode == config.MemGuardOff || g.Mode == "" {
		return false
	}
	if g.Method == config.MethodWrapper {
		return false
	}
	// auto and scope both require the mechanism to actually exist.
	return memscopeAvailable()
}

// Wrap returns the binary, argv, and scope unit name to use for a spawn.
// An empty unit name means the spawn was not wrapped, and the caller must
// exec exactly what it passed in.
func (g *MemGuard) Wrap(bin string, args []string, providerName string, seq int) (string, []string, string) {
	if !g.wraps() {
		return bin, args, ""
	}
	l := log.With().Str("component", "memguard").Logger()

	if err := memscope.EnsureSlice(g.sliceLimits()); err != nil {
		// A missing slice costs the aggregate ceiling, but the per-scope
		// ceiling still applies. Degrade rather than refuse to spawn —
		// an unguarded agent beats no agent at all.
		l.Warn().Err(err).Msg("could not ensure agents.slice; per-scope limits still apply")
	}

	limit := g.AgentLimitMB
	if g.Mode == config.MemGuardMeasure {
		// Measure records peaks and changes nothing else: the scope exists
		// so memory.peak is readable, but nothing is ever killed for it.
		limit = 0
	}

	unit := memscope.ScopeUnitName(providerName, seq)
	wbin, wargv := memscope.WrapArgv(bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limit,
	})
	l.Debug().Str("unit", unit).Int("limit_mb", limit).Msg("agent spawn wrapped in scope")
	return wbin, wargv, unit
}

// BiasChild pushes the kernel toward killing this agent rather than wick.
//
// Advisory by design: the agent is already running by the time this can
// run, so a failure is logged and never turned into a spawn error.
func (g *MemGuard) BiasChild(pid int) {
	if g == nil || g.Mode != config.MemGuardEnforce || !g.ProtectWick || pid <= 0 {
		return
	}
	if err := oomscore.Adjust(pid, oomscore.AgentScore); err != nil {
		l := log.With().Str("component", "memguard").Logger()
		l.Debug().Err(err).Int("pid", pid).Msg("could not bias agent oom score")
	}
}

// ClassifyExit reports whether this exit was an OOM kill, with a sentence
// naming the numbers.
//
// The bool is false whenever there is no evidence. An exit code alone
// cannot distinguish an OOM kill from any other SIGKILL, and --collect
// reaps a scope as soon as its last process exits, so a guess here would
// mislabel the very failure this exists to explain.
func (g *MemGuard) ClassifyExit(unit string, limitMB int) (ExitReason, string, bool) {
	if g == nil || unit == "" {
		return ExitError, "", false
	}
	st := memscope.ReadStats(unit)
	if !st.Known || st.OOMKills == 0 {
		return ExitError, "", false
	}
	return ExitOOM, OOMDetail(st.PeakBytes, limitMB), true
}
