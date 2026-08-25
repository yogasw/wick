package provider

import (
	"os"
	"sync/atomic"

	"github.com/rs/zerolog"
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
	Mode string // config.MemGuard{Off,Measure,Enforce}
	// Scopes is WHERE the limit is applied. Both can be on at once: the
	// kernel applies every ceiling in the hierarchy and the tightest
	// wins, and on a real host the combination covers more than either
	// alone — see config.GuardScopes.
	Scopes       config.GuardScopes
	AgentLimitMB int // resolved per-agent ceiling; 0 = none
	AggregateMB  int // slice-wide ceiling; 0 = none
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

// memscopeBackend is a seam so tests can drive every branch without a
// systemd user session or a real cgroup mount. Production always points
// at memscope.DetectBackend, which ranks systemd-run above raw cgroupfs
// and caches whichever it finds working — see memscope/backend_linux.go.
var memscopeBackend = memscope.DetectBackend

// removeCgroupScope is the same kind of seam for scope teardown: a test
// asserting that release happens must not remove directories from the
// real host hierarchy to prove it.
var removeCgroupScope = memscope.RemoveCgroupScope

// selfExecutable resolves the path to wick's own running binary. Only the
// cgroupfs backend needs it — memscope.WrapArgvCgroupFS re-execs wick
// itself as the wrapper, since nothing else on a systemd-less host
// performs "create a cgroup, join it, then exec the real binary" for us.
// Indirected so a test can supply a path without depending on
// os.Executable succeeding in whatever sandbox runs it.
var selfExecutable = os.Executable

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
// With OnSpawn off, the operator has said something else covers these
// agents — a shim on the path — so wick only measures. Both scopes on at
// once is fine and often better: the kernel applies every ceiling and the
// tightest wins, so the shim reaches callers wick cannot see while wick
// still covers its own agents if the shim's link is ever replaced.
func (g *MemGuard) wraps() bool {
	if g == nil || g.Mode == config.MemGuardOff || g.Mode == "" {
		return false
	}
	// OnPath is handled by a shim in front of the binary, not here.
	// Without OnSpawn there is nothing for wick itself to do — which is
	// not the same as nothing being applied.
	if !g.Scopes.OnSpawn {
		return false
	}
	// Wrapping needs a mechanism to actually exist — systemd or, failing
	// that, raw cgroupfs. See memscope.DetectBackend.
	return memscopeBackend() != memscope.BackendNone
}

// Wrap returns the binary, argv, and scope unit name to use for a spawn.
// An empty unit name means the spawn was not wrapped, and the caller must
// exec exactly what it passed in.
func (g *MemGuard) Wrap(bin string, args []string, providerName string, seq int) (string, []string, string) {
	if !g.wraps() {
		return bin, args, ""
	}
	l := log.With().Str("component", "memguard").Logger()

	limit := g.AgentLimitMB
	if g.Mode == config.MemGuardMeasure {
		// Measure records peaks and changes nothing else: the scope exists
		// so a peak is readable, but nothing is ever killed for it.
		limit = 0
	}
	unit := memscope.ScopeUnitName(providerName, seq)

	if memscopeBackend() == memscope.BackendCgroupFS {
		return g.wrapCgroupFS(l, bin, args, unit, limit)
	}
	return g.wrapSystemd(l, bin, args, unit, limit)
}

// wrapSystemd is the original path: a systemd-run --user --scope wrapper.
func (g *MemGuard) wrapSystemd(l zerolog.Logger, bin string, args []string, unit string, limit int) (string, []string, string) {
	if err := memscope.EnsureSlice(g.sliceLimits()); err != nil {
		// A missing slice costs the aggregate ceiling, but the per-scope
		// ceiling still applies. Degrade rather than refuse to spawn —
		// an unguarded agent beats no agent at all.
		l.Warn().Err(err).Msg("could not ensure agents.slice; per-scope limits still apply")
	}
	wbin, wargv := memscope.WrapArgv(bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limit,
	})
	l.Debug().Str("unit", unit).Int("limit_mb", limit).Str("backend", "systemd").
		Msg("agent spawn wrapped in scope")
	return wbin, wargv, unit
}

// wrapCgroupFS is the systemd-less fallback: wick re-execs itself through
// the hidden __agent-exec subcommand, which drives cgroup v1 directly.
// See memscope/cgroupfs_linux.go for why this exists and what it cannot
// report as confidently as the systemd path (no per-group OOM-kill
// counter).
func (g *MemGuard) wrapCgroupFS(l zerolog.Logger, bin string, args []string, unit string, limit int) (string, []string, string) {
	if err := memscope.EnsureCgroupSlice(g.sliceLimits()); err != nil {
		l.Warn().Err(err).Msg("could not ensure agents cgroup slice; per-scope limits still apply")
	}
	self, err := selfExecutable()
	if err != nil {
		// No self path, no wrapper to re-exec through. Degrade the same
		// way an EnsureSlice failure does elsewhere in this file: an
		// unguarded agent beats refusing to spawn one at all.
		l.Warn().Err(err).Msg("could not resolve wick's own binary path; spawning unguarded")
		return bin, args, ""
	}
	wbin, wargv := memscope.WrapArgvCgroupFS(self, bin, args, memscope.Opts{
		Unit: unit, Slice: memscope.SliceName, LimitMB: limit,
	})
	l.Debug().Str("unit", unit).Int("limit_mb", limit).Str("backend", "cgroupfs").
		Msg("agent spawn wrapped in raw cgroup")
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

// ReleaseScope removes the cgroup a spawn ran inside, once nothing is
// left to read from it.
//
// Only the cgroupfs backend needs this. systemd-run is passed --collect,
// which reaps its scope the moment the last process exits; raw cgroupfs
// has no daemon behind it, so without this every spawn leaves a permanent
// directory behind — and scope names carry an increasing sequence, so
// they accumulate rather than being reused.
//
// Separate from ClassifyExit rather than folded into it, because that
// runs only for an exit already believed to be an error: a clean exit
// never reaches it, and a clean exit leaks a directory just the same.
//
// Best-effort and never fatal. A scope that still holds a process refuses
// to be removed (EBUSY), which is the right outcome — an agent that
// outlived its wick stays contained.
func (g *MemGuard) ReleaseScope(unit string) {
	if g == nil || unit == "" {
		return
	}
	if memscopeBackend() != memscope.BackendCgroupFS {
		return
	}
	if err := removeCgroupScope(unit); err != nil {
		l := log.With().Str("component", "memguard").Logger()
		l.Debug().Err(err).Str("unit", unit).Msg("could not remove agent cgroup scope")
	}
}
