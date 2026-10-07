package provider

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/procgroup"
	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// helperguard.go puts the short-lived helper processes wick starts from an
// omp/opencode (or any provider) binary — `omp models --json`, `opencode
// models`, opencode export/import, `omp usage`, auth-broker listings,
// version probes — under the same memory guard an agent spawn gets: one
// scope each, named after what it is (memscope.ScopeUnitName(label, seq)),
// with the instance's own per-agent limit (Instance.MemoryMaxMB, else the
// guard's agent_memory_max_mb — config.ResolveAgentLimitMB, never a min).
// Before this they ran as plain children of wick: no limit, badge "wick".
//
// The guard semantics are the agent spawn's, unchanged: off = no wrap at
// all; measure = a scope with no limit (peaks recorded); enforce = the
// limit. MemGuard.Wrap decides; this file only resolves the limit.

// HelperGuard returns the current guard policy (nil = off). Set once at
// startup to the pool factory's MemGuardLoader (internal/pkg/api), the
// same policy agent spawns read per Build.
var HelperGuard func() *MemGuard

// HelperCommand builds cmd for one helper process of ins, wrapped by the
// memory guard with ins's own limit. ins may be nil (no instance known):
// the guard's default limit applies. release must run once the process
// has ended.
//
// Every helper is bounded and leaves nothing behind (bun forks): it runs
// under a timeout (helperTimeout: short for a --version probe), a timeout
// or cancel kills the whole process group, not just the leader, and
// release kills the group again — any child the helper left running —
// before the guard scope is released.
func HelperCommand(ctx context.Context, ins *Instance, label, bin string, args ...string) (*exec.Cmd, func()) {
	if fn := helperObserver.Load(); fn != nil {
		(*fn)(label, ins)
	}
	timeout := helperTimeout(ins, label)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	seq := nextSpawnSeq()
	var g *MemGuard
	if HelperGuard != nil {
		g = HelperGuard()
	}
	releaseScope := func() {}
	execBin, execArgs, unit := bin, args, ""
	if g != nil {
		r := *g
		if ins != nil {
			r.AgentLimitMB = config.ResolveAgentLimitMB(ins.MemoryMaxMB, g.AgentLimitMB)
		}
		wb, wa, u := r.Wrap(bin, args, label, seq)
		execBin, execArgs, unit = wb, wa, u
		if unit != "" {
			releaseScope = func() { r.ReleaseScope(u) }
			log.Debug().Str("component", "memguard").Str("label", label).Str("unit", unit).
				Int("limit_mb", r.AgentLimitMB).Msg("helper process wrapped")
		}
	}
	cmd := safeexec.CommandContext(ctx, execBin, execArgs...)
	// Never the daemon environment (DATABASE_URL and friends): callers that
	// need an account env overwrite this with their own.
	cmd.Env = envscrub.ScrubOSEnv()
	procgroup.Apply(cmd)
	// On timeout / cancel: the whole group, so a forked child cannot keep
	// running (or keep the output pipe open: WaitDelay bounds that).
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	insName := ""
	if ins != nil {
		insName = string(ins.Type) + "/" + ins.Name
	}
	now := helperNow()
	registerHelper(HelperRecord{Seq: seq, Label: label, Instance: insName, Unit: unit, Start: now, Deadline: now.Add(timeout)}, cancel, releaseScope)
	var once sync.Once
	release := func() {
		once.Do(func() {
			unregisterHelper(seq)
			cancel()
			if cmd.Process != nil {
				killGroup(cmd.Process.Pid) // leftovers of a finished helper
			}
			releaseScope()
		})
	}
	return cmd, release
}

// Helper timeouts. A --version probe answers in well under a second when
// healthy; omp/opencode boot bun, so they get a little more. Anything
// else (model listing, export/import, usage) is capped too — a helper
// that hangs must not hold the listing slot or its memory forever.
const (
	heavyVersionTimeout = 10 * time.Second
	lightVersionTimeout = 5 * time.Second
	helperMaxTimeout    = 2 * time.Minute
)

func helperTimeout(ins *Instance, label string) time.Duration {
	if strings.HasSuffix(label, "-version") {
		if ins != nil && heavyProbe(ins.Type) {
			return heavyVersionTimeout
		}
		return lightVersionTimeout
	}
	return helperMaxTimeout
}

// helperSlot serializes model-listing helpers across ALL instances: each
// `omp models` / `opencode models` / throwaway `opencode serve` is a
// 150–500 MB process, and a page warming every instance at once used to
// start them in one burst. Taken at the process start (never around a
// call that may itself start one), so it cannot nest.
var helperSlot = make(chan struct{}, 1)

// AcquireHelperSlot waits for the model-listing slot; the returned func
// frees it. ctx cancels the wait.
func AcquireHelperSlot(ctx context.Context) (func(), error) {
	select {
	case helperSlot <- struct{}{}:
		return func() { <-helperSlot }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// HelperWrap is HelperCommand's guard step for a caller that builds its
// own command (a server started through a cliserver start function):
// wrap goes where a spawn passes MemGuard.Wrap; release runs after the
// process ended.
func HelperWrap(ins *Instance, label string) (wrap func(bin string, args []string) (string, []string, string), release func()) {
	var g *MemGuard
	if HelperGuard != nil {
		g = HelperGuard()
	}
	if g == nil {
		return nil, func() {}
	}
	r := *g
	if ins != nil {
		r.AgentLimitMB = config.ResolveAgentLimitMB(ins.MemoryMaxMB, g.AgentLimitMB)
	}
	var unit string
	return func(bin string, args []string) (string, []string, string) {
			wb, wa, u := r.Wrap(bin, args, label, nextSpawnSeq())
			unit = u
			return wb, wa, u
		}, func() {
			if unit != "" {
				r.ReleaseScope(unit)
			}
		}
}

// helperObserver sees every helper process built (tests only).
var helperObserver atomic.Pointer[func(label string, ins *Instance)]

// ObserveHelpersForTest calls fn for every HelperCommand (label, the
// instance whose limit applies); the returned func stops it. Tests only:
// it is how other packages prove their exec paths go through the guard.
func ObserveHelpersForTest(fn func(label string, ins *Instance)) (restore func()) {
	helperObserver.Store(&fn)
	return func() { helperObserver.Store(nil) }
}

// HelperLabel is "<type>-<what>" ("omp-models", "opencode-export").
func HelperLabel(t Type, what string) string { return string(t) + "-" + what }

// InstanceForAccountEnv finds the instance an AccountEnv belongs to (its
// omp profile / opencode data dir), so a helper started from that env
// alone still gets the instance's limit. nil when none matches.
func InstanceForAccountEnv(env []string) *Instance {
	profile, dataDir := "", ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "OMP_PROFILE="); ok {
			profile = v
		}
		if v, ok := strings.CutPrefix(kv, "XDG_DATA_HOME="); ok {
			dataDir = v
		}
	}
	if profile == "" && dataDir == "" {
		return nil
	}
	instances, err := Load()
	if err != nil {
		return nil
	}
	for i := range instances {
		ins := instances[i]
		switch {
		case ins.Type == TypeOMP && profile != "" && OMPProfile(ins) == profile:
			return &ins
		case ins.Type == TypeOpencode && dataDir != "":
			if d, err := OpencodeDataDir(ins); err == nil && d == dataDir {
				return &ins
			}
		}
	}
	return nil
}

// IdleYielder stops a package's idle warm servers (omp RPC, omp auth
// broker, opencode serve) that keep does not protect; returns how many.
type IdleYielder func(keep func(instance, group string) bool) int

var (
	idleYieldersMu sync.Mutex
	idleYielders   = map[string]IdleYielder{}
)

// RegisterIdleYielder installs a package's yielder under name (init).
// nil removes it (tests).
func RegisterIdleYielder(name string, y IdleYielder) {
	idleYieldersMu.Lock()
	defer idleYieldersMu.Unlock()
	if y == nil {
		delete(idleYielders, name)
		return
	}
	idleYielders[name] = y
}

// YieldIdleServers makes room for a spawn about to start for sessionID on
// instance: every idle warm server goes, except the ones that spawn will
// use — the session's own (group "<instance>/<session>") and those of the
// instance it runs on. Busy or queued servers are never stopped. Returns
// how many were stopped.
func YieldIdleServers(sessionID, instance string) int {
	keep := func(inst, group string) bool {
		if instance != "" && inst == instance {
			return true
		}
		return sessionID != "" && strings.HasSuffix(group, "/"+sessionID)
	}
	idleYieldersMu.Lock()
	ys := make([]IdleYielder, 0, len(idleYielders))
	for _, y := range idleYielders {
		ys = append(ys, y)
	}
	idleYieldersMu.Unlock()
	n := 0
	for _, y := range ys {
		n += y(keep)
	}
	if n > 0 {
		log.Info().Str("component", "pool").Str("session", sessionID).Str("instance", instance).Int("stopped", n).
			Msg("idle provider servers stopped to make room for a spawn")
	}
	return n
}
