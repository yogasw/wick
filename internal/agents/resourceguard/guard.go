// Package resourceguard is wick's fast watchdog for every agent it spawns
// — sessions, sub-agents, Team agents, workflow agent nodes, and whatever
// they start, including `systemd-run --user` units outside agents.slice.
//
// One rule drives it: when the host nears a hang, stop agent child
// processes one at a time until BOTH host CPU and host memory are back
// under the safe line (80% by default). The kernel's own limits and OOM
// killer stay as the backstop, but on a small host without swap they act
// after the machine has already stopped answering: memory pressure ran
// for minutes with no OOM kill before an incident this was built for.
package resourceguard

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// Actions, from mildest to strongest. Action config caps what the guard
// may do; measure mode records only regardless.
const (
	ActionOff   = "off"
	ActionLog   = "log"
	ActionPause = "pause"
	ActionKill  = "kill"
)

// Fixed thresholds. They encode how fast a small host goes from "busy"
// to "unreachable"; the safe line, the projection horizon and the CPU
// pressure threshold are the knobs an operator has a reason to move.
const (
	trendWindow     = 10 * time.Second
	actCooldown     = 2 * time.Second // re-measure between kills
	memDangerPct    = 90
	cpuDangerPct    = 95
	cpuHotFor       = 10 * time.Second
	psiMemFullMax   = 20
	calmToRelease   = 30 * time.Second // resume paused work / restore quota
	cpuThrottlePct  = 100
	tickLagMax      = time.Second
	historyMax      = 100
	defaultSafePct  = 80
	defaultPSICPU   = 90
	defaultInterval = time.Second
	// outsideShareMax: when agents use less than this share of the busy
	// CPU (or of the used memory), the load comes from outside wick and
	// stopping agents would not bring the host back.
	outsideShareMax = 30
	usecPerTick     = 10_000 // /proc/stat jiffies are USER_HZ (100/s)
)

// childPattern is what counts as a disposable child: build tools, test
// runners, browsers, scripts.
var childPattern = regexp.MustCompile(`^(node|esbuild|vite|vitest|go|compile|link|asm|cgo|gopls|chrome|chromium|headless_shell|python3?|npm|npx|pnpm|yarn|bun|deno|tsc|cargo|rustc|gcc|cc1|cc1plus|ld|make|java|playwright)$`)

// neverTouch matches command lines the guard must never act on: the
// agent CLIs and wick itself.
var neverTouch = regexp.MustCompile(`(?i)\b(claude|codex|support-tools|wick|opencode|gemini)\b`)

// Config is read fresh on every tick so a change in the UI applies
// without a restart.
type Config struct {
	Enabled     bool   // memory_guard_mode is enforce or measure, on Linux
	Measure     bool   // measure mode: record what would be done, do nothing
	Action      string // off|log|pause|kill
	Interval    time.Duration
	SafePct     int     // stop acting once host CPU and memory are both below this
	HorizonSec  int     // memory projected to run out sooner than this = near hang
	MinFreeMB   int     // hard floor
	CPUPSIMax   float64 // cpu "some" avg10 above this = tasks are queueing
	CPUQuotaPct int     // configured slice quota, restored after a throttle
	CPUWeight   int
	TasksMax    int
}

// Event is one guard action, kept for the Resources page and pushed to
// OnEvent.
type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // kill_child|stop_child|cont_child|kill_scope|freeze|thaw|throttle|restore|near_hang|resolved|outside_busy
	Scope  string    `json:"scope,omitempty"`
	PID    int       `json:"pid,omitempty"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail"`
	// AgentPID is the agent CLI that owns the scope, so the caller can
	// tell that agent's session what happened. 0 = unknown (a run-* unit).
	AgentPID int `json:"agent_pid,omitempty"`
	// DryRun marks a record of what WOULD have been done (measure/log).
	DryRun bool `json:"dry_run,omitempty"`
}

// Guard is the watchdog. Use New.
type Guard struct {
	host    Host
	load    func() Config
	OnEvent func(Event)
	// OnQuotaApplied runs after configured slice limits are written live,
	// so the caller can persist them where a reload reads them.
	OnQuotaApplied func()
	now            func() time.Time

	mu      sync.Mutex
	history []Event
	hold    bool

	samples    []sample
	prevCPU    map[int]uint64
	prevAt     time.Time
	prevBusy   uint64
	prevTotal  uint64
	scopeAgent map[string]int
	dryRun     bool
	lastTick   time.Time

	incident     bool
	lastAct      time.Time
	cpuHotSince  time.Time
	calmSince    time.Time
	throttled    bool
	stopped      map[int]bool    // SIGSTOPped children (pause action)
	frozen       map[string]bool // frozen scopes (pause action)
	appliedQuota *[3]int

	prevAgentUsec uint64
	outside       bool   // an outside-wick episode was already reported
	topOutside    string // busiest process outside wick, e.g. "bob/compile 70% CPU"
}

// New builds a guard over host, reading its config through load.
func New(host Host, load func() Config) *Guard {
	return &Guard{
		host: host, load: load, now: time.Now,
		prevCPU: map[int]uint64{}, stopped: map[int]bool{}, frozen: map[string]bool{},
	}
}

// Run ticks until ctx ends. A nil guard or host is a no-op.
func (g *Guard) Run(ctx context.Context) {
	if g == nil || g.host == nil {
		return
	}
	for {
		interval := g.load().Interval
		if interval <= 0 {
			interval = defaultInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		g.Tick()
	}
}

// History returns the most recent actions, newest last.
func (g *Guard) History() []Event {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Event(nil), g.history...)
}

// HoldSpawns reports whether a new agent should wait: the host is near a
// hang, or memory is heading there. A static free-memory floor cannot see
// a host losing 200 MB/s with 900 MB left.
func (g *Guard) HoldSpawns() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hold
}

func (g *Guard) setHold(v bool) {
	g.mu.Lock()
	g.hold = v
	g.mu.Unlock()
}

func (g *Guard) emit(e Event) {
	e.At = g.now()
	if e.AgentPID == 0 && e.Scope != "" {
		e.AgentPID = g.scopeAgent[e.Scope]
	}
	switch e.Kind {
	case "kill_child", "stop_child", "kill_scope", "freeze", "throttle":
		if g.dryRun {
			e.DryRun = true
			e.Detail = "[measure only, nothing done] would have: " + e.Detail
		}
	}
	g.mu.Lock()
	g.history = append(g.history, e)
	if len(g.history) > historyMax {
		g.history = g.history[len(g.history)-historyMax:]
	}
	g.mu.Unlock()
	log.Warn().Str("component", "resourceguard").Str("kind", e.Kind).Str("scope", e.Scope).
		Int("pid", e.PID).Str("target", e.Target).Bool("dry_run", e.DryRun).Msg(e.Detail)
	if g.OnEvent != nil {
		g.OnEvent(e)
	}
}

// may reports whether the guard may really take an action of strength
// need; false means it only records.
func (g *Guard) may(cfg Config, need string) bool {
	if g.dryRun {
		return false
	}
	rank := map[string]int{ActionLog: 1, ActionPause: 2, ActionKill: 3}
	return rank[cfg.Action] >= rank[need]
}

// hostState is one sample of the whole host.
type hostState struct {
	memUsedPct float64
	availMB    int
	eta        float64 // seconds to memory exhaustion at the current slope
	busyPct    float64
	psiMemFull float64
	psiCPU     float64
	running    int
	cores      int
	lagging    bool
	// agentCPUPct is the agents' share of the busy CPU since the last
	// sample; agentCPUOK is false when it could not be measured.
	agentCPUPct float64
	agentCPUOK  bool
	totalTicks  uint64 // all-core jiffies since the last sample
}

func (g *Guard) sample(cfg Config, now time.Time) (hostState, bool) {
	var st hostState
	avail, total, ok := g.host.MemAvailable()
	if !ok || total <= 0 {
		return st, false
	}
	st.availMB = avail
	st.memUsedPct = float64(total-avail) * 100 / float64(total)
	g.samples = trimWindow(append(g.samples, sample{at: now, availMB: float64(avail)}), now, trendWindow)
	st.eta = secondsToExhaustion(float64(avail), slopeMBps(g.samples))

	agentUsec, agentOK := g.host.AgentCPUUsec()
	if busy, tot, ok := g.host.CPUTimes(); ok {
		if g.prevTotal != 0 && tot > g.prevTotal && busy >= g.prevBusy {
			st.busyPct = float64(busy-g.prevBusy) * 100 / float64(tot-g.prevTotal)
			st.totalTicks = tot - g.prevTotal
			if agentOK && g.prevAgentUsec != 0 && agentUsec >= g.prevAgentUsec && busy > g.prevBusy {
				st.agentCPUPct = float64(agentUsec-g.prevAgentUsec) * 100 / float64((busy-g.prevBusy)*usecPerTick)
				st.agentCPUOK = true
			}
		}
		g.prevBusy, g.prevTotal = busy, tot
	}
	if agentOK {
		g.prevAgentUsec = agentUsec
	}
	_, st.psiMemFull, _ = g.host.PSI("memory")
	st.psiCPU, _, _ = g.host.PSI("cpu")
	st.running = g.host.ProcsRunning()
	st.cores = g.host.NumCPU()
	// wick's own responsiveness: a tick that fires a second late means the
	// daemon itself is starved.
	if !g.lastTick.IsZero() && cfg.Interval > 0 && now.Sub(g.lastTick)-cfg.Interval > tickLagMax {
		st.lagging = true
	}
	return st, true
}

// Tick runs one sample-decide-act pass. Exported for tests.
func (g *Guard) Tick() {
	cfg := g.load()
	now := g.now()
	defer func() { g.lastTick = now }()
	if !cfg.Enabled || cfg.Action == ActionOff || cfg.Action == "" {
		g.setHold(false)
		return
	}
	if cfg.SafePct <= 0 || cfg.SafePct > 100 {
		cfg.SafePct = defaultSafePct
	}
	if cfg.CPUPSIMax <= 0 {
		cfg.CPUPSIMax = defaultPSICPU
	}
	g.dryRun = cfg.Measure || cfg.Action == ActionLog
	g.applyQuota(cfg)

	st, ok := g.sample(cfg, now)
	scopes := g.host.Scopes()
	procs := g.readProcs(scopes, now)
	defer func() {
		g.prevAt = now
		g.prevCPU = map[int]uint64{}
		for _, p := range procs {
			g.prevCPU[p.PID] = p.CPUTicks
		}
	}()
	if !ok {
		return
	}
	safe := float64(cfg.SafePct)
	horizon := float64(cfg.HorizonSec)

	// Memory near hang: very full, under the floor, projected to run out
	// soon while already above the safe line, or stalling on reclaim.
	memReason := ""
	switch {
	case st.memUsedPct >= memDangerPct:
		memReason = fmt.Sprintf("memory %.0f%% used", st.memUsedPct)
	case cfg.MinFreeMB > 0 && st.availMB < cfg.MinFreeMB:
		memReason = fmt.Sprintf("%d MB free, below the %d MB floor", st.availMB, cfg.MinFreeMB)
	case horizon > 0 && st.eta < horizon && st.memUsedPct >= safe:
		memReason = fmt.Sprintf("memory %.0f%% used and falling — out in ~%.0fs", st.memUsedPct, st.eta)
	case st.psiMemFull > psiMemFullMax:
		memReason = fmt.Sprintf("memory pressure full avg10 %.0f%%", st.psiMemFull)
	}
	// CPU near hang: busy AND tasks queueing. Busy alone is a build using
	// idle CPU, which is fine.
	queueing := st.psiCPU > cfg.CPUPSIMax || (st.cores > 0 && st.running > 2*st.cores)
	if st.busyPct >= cpuDangerPct && queueing {
		if g.cpuHotSince.IsZero() {
			g.cpuHotSince = now
		}
	} else {
		g.cpuHotSince = time.Time{}
	}
	cpuReason := ""
	if st.lagging {
		cpuReason = fmt.Sprintf("wick itself is responding slowly (CPU %.0f%%)", st.busyPct)
	} else if !g.cpuHotSince.IsZero() && now.Sub(g.cpuHotSince) >= cpuHotFor {
		cpuReason = fmt.Sprintf("CPU %.0f%% busy with pressure %.0f%% for %s", st.busyPct, st.psiCPU, now.Sub(g.cpuHotSince).Round(time.Second))
	}

	if !g.cpuHotSince.IsZero() || st.lagging || memReason != "" {
		g.topOutside = g.busiestOutside(procs, st)
	}
	if memReason == "" && cpuReason == "" {
		g.outside = false
	} else if memPct, ok := g.fromOutside(st, scopes, procs, memReason, cpuReason); ok {
		// The load is not ours: stopping agents would not bring the host
		// back. Leave them running, hold spawns only when memory is
		// really running out, and undo any earlier step once calm.
		if !g.outside {
			g.outside = true
			top := g.topOutside
			if top == "" {
				top = "unknown process"
			}
			g.emit(Event{Kind: "outside_busy", Detail: fmt.Sprintf("host busy from outside wick: %s (agents use %.0f%% of busy CPU, %.0f%% of used memory), agents left running", top, st.agentCPUPct, memPct)})
		}
		g.setHold(memReason != "")
		g.incident = false
		g.release(cfg, now, false)
		return
	}

	g.setHold(memReason != "" || g.incident ||
		(horizon > 0 && st.eta < 2*horizon && st.memUsedPct >= safe))

	aboveSafe := st.memUsedPct >= safe || st.busyPct >= safe
	if !g.incident && (memReason != "" || cpuReason != "") {
		g.incident = true
		reason := strings.TrimPrefix(memReason+"; "+cpuReason, "; ")
		reason = strings.TrimSuffix(reason, "; ")
		g.emit(Event{Kind: "near_hang", Detail: fmt.Sprintf("host near a hang (%s); stopping agent work until CPU and memory are under %d%%", reason, cfg.SafePct)})
		// A brief first step for CPU: cap the agents while the hog is found.
		if cpuReason != "" && !g.throttled {
			if g.may(cfg, ActionPause) {
				_ = g.host.SetSliceCPU(cpuThrottlePct, cfg.CPUWeight, cfg.TasksMax)
				g.throttled = true
			}
			g.emit(Event{Kind: "throttle", Detail: fmt.Sprintf("agent CPU capped at %d%% while the host recovers", cpuThrottlePct)})
		}
	}

	if g.incident && !aboveSafe {
		g.incident = false
		g.emit(Event{Kind: "resolved", Detail: fmt.Sprintf("host back under %d%% (CPU %.0f%%, memory %.0f%%)", cfg.SafePct, st.busyPct, st.memUsedPct)})
	}
	if g.incident {
		g.calmSince = time.Time{}
		if now.Sub(g.lastAct) >= actCooldown {
			g.actOnce(cfg, st, scopes, procs)
			g.lastAct = now
		}
		return
	}
	g.release(cfg, now, aboveSafe)
}

// fromOutside reports whether every reason the host is near a hang comes
// from outside wick: agents hold under outsideShareMax of the busy CPU
// (for a CPU reason) and of the used memory (for a memory reason). An
// unmeasured CPU share is never "outside", so the guard keeps its old
// behaviour where it cannot tell. memPct is the agents' memory share.
func (g *Guard) fromOutside(st hostState, scopes []Scope, procs []agentProc, memReason, cpuReason string) (memPct float64, outside bool) {
	memPct = agentMemPct(g.host, st, scopes, procs)
	if cpuReason != "" && (!st.agentCPUOK || st.agentCPUPct >= outsideShareMax) {
		return memPct, false
	}
	if memReason != "" && memPct >= outsideShareMax {
		return memPct, false
	}
	return memPct, true
}

// agentMemPct is the agents' share of the host's used memory. A scope
// counts as the larger of its cgroup memory and its processes' RSS.
func agentMemPct(h Host, st hostState, scopes []Scope, procs []agentProc) float64 {
	_, totalMB, ok := h.MemAvailable()
	usedMB := float64(totalMB - st.availMB)
	if !ok || usedMB <= 0 {
		return 0
	}
	rss := map[string]uint64{}
	for _, p := range procs {
		rss[p.scope] += p.RSSBytes
	}
	self := h.SelfScope()
	var agents uint64
	for _, s := range scopes {
		if s.Name != self {
			agents += max(s.MemBytes, rss[s.Name])
		}
	}
	return float64(agents) / (1 << 20) * 100 / usedMB
}

// busiestOutside names the process outside every agent scope that used
// the most CPU since the last call, e.g. "bob/compile 70% CPU"; "" when
// unknown.
func (g *Guard) busiestOutside(procs []agentProc, st hostState) string {
	skip := map[int]bool{}
	for _, p := range procs {
		skip[p.PID] = true
	}
	p, user, t, ok := g.host.BusiestOutside(skip)
	if !ok || st.totalTicks == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%s %.0f%% CPU", user, p.Comm, min(100, float64(t)*100/float64(st.totalTicks)))
}

// actOnce stops ONE thing, chosen by what is short: memory → the largest
// resident child; CPU → the child that used the most CPU since the last
// sample. With no child left, the heaviest agent scope goes.
func (g *Guard) actOnce(cfg Config, st hostState, scopes []Scope, procs []agentProc) {
	byMem := st.memUsedPct >= float64(cfg.SafePct)
	why := fmt.Sprintf("CPU %.0f%%, memory %.0f%%", st.busyPct, st.memUsedPct)
	if p, ok := pickChild(procs, byMem, g.stopped); ok {
		if g.may(cfg, ActionKill) {
			_ = g.host.Signal(p.PID, syscall.SIGKILL)
		} else if g.may(cfg, ActionPause) {
			_ = g.host.Signal(p.PID, syscall.SIGSTOP)
			g.stopped[p.PID] = true
			g.emit(Event{Kind: "stop_child", Scope: p.scope, PID: p.PID, Target: describe(p),
				Detail: fmt.Sprintf("wick paused `%s` (%s) to keep the host alive (%s)", describe(p), gb(p.RSSBytes), why)})
			return
		}
		g.emit(Event{Kind: "kill_child", Scope: p.scope, PID: p.PID, Target: describe(p),
			Detail: fmt.Sprintf("wick stopped `%s` (%s) to keep the host alive (%s); re-run it when the host is quieter", describe(p), gb(p.RSSBytes), why)})
		return
	}
	s, ok := g.pickScope(scopes, procs, byMem)
	if !ok {
		return
	}
	if g.may(cfg, ActionKill) {
		_ = g.host.KillScope(s.Name)
	} else if g.may(cfg, ActionPause) {
		_ = g.host.Freeze(s.Name, true)
		g.frozen[s.Name] = true
		g.emit(Event{Kind: "freeze", Scope: s.Name,
			Detail: fmt.Sprintf("wick paused agent %s (%s) to keep the host alive (%s)", s.Name, gb(s.MemBytes), why)})
		return
	}
	g.emit(Event{Kind: "kill_scope", Scope: s.Name,
		Detail: fmt.Sprintf("wick stopped agent %s (%s) to keep the host alive (%s); the conversation resumes on its next message", s.Name, gb(s.MemBytes), why)})
}

// release undoes the temporary steps once the host has been calm.
func (g *Guard) release(cfg Config, now time.Time, aboveSafe bool) {
	if aboveSafe {
		g.calmSince = time.Time{}
		return
	}
	if g.calmSince.IsZero() {
		g.calmSince = now
	}
	if now.Sub(g.calmSince) < calmToRelease {
		return
	}
	for pid := range g.stopped {
		if _, alive := g.host.Proc(pid); alive {
			_ = g.host.Signal(pid, syscall.SIGCONT)
			g.emit(Event{Kind: "cont_child", PID: pid, Detail: fmt.Sprintf("host calm; resumed pid %d", pid)})
		}
		delete(g.stopped, pid)
	}
	for s := range g.frozen {
		_ = g.host.Freeze(s, false)
		g.emit(Event{Kind: "thaw", Scope: s, Detail: "host calm; resumed agent " + s})
		delete(g.frozen, s)
	}
	if g.throttled {
		_ = g.host.SetSliceCPU(cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax)
		g.throttled = false
		q := [3]int{cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax}
		g.appliedQuota = &q
		g.emit(Event{Kind: "restore", Detail: "host calm; agent CPU quota back to " + quotaLabel(cfg.CPUQuotaPct)})
	}
}

// applyQuota writes the configured slice CPU controls when they change —
// the "live" in live quota. Skipped while a throttle is in force; release
// writes the configured value back.
func (g *Guard) applyQuota(cfg Config) {
	if g.throttled || cfg.Measure {
		return
	}
	want := [3]int{cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax}
	if g.appliedQuota != nil && *g.appliedQuota == want {
		return
	}
	if err := g.host.SetSliceCPU(cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax); err != nil {
		log.Debug().Err(err).Str("component", "resourceguard").Msg("could not apply slice CPU limits")
		return
	}
	g.appliedQuota = &want
	if g.OnQuotaApplied != nil {
		g.OnQuotaApplied()
	}
}

// agentProc is a process with its scope and CPU used since last sample.
type agentProc struct {
	Proc
	scope    string
	isAgent  bool // the agent CLI itself (the scope's root process)
	cpuTicks uint64
}

func (g *Guard) readProcs(scopes []Scope, now time.Time) []agentProc {
	self := g.host.SelfScope()
	var out []agentProc
	g.scopeAgent = map[string]int{}
	for _, s := range scopes {
		if s.Name == self {
			continue
		}
		in := map[int]bool{}
		for _, pid := range s.PIDs {
			in[pid] = true
		}
		for _, pid := range s.PIDs {
			p, ok := g.host.Proc(pid)
			if !ok {
				continue
			}
			ap := agentProc{Proc: p, scope: s.Name, isAgent: !s.Detached && !in[p.PPID]}
			if ap.isAgent && g.scopeAgent[s.Name] == 0 {
				g.scopeAgent[s.Name] = pid
			}
			if prev, ok := g.prevCPU[pid]; ok && p.CPUTicks >= prev {
				ap.cpuTicks = p.CPUTicks - prev
			}
			out = append(out, ap)
		}
	}
	return out
}

// disposable is a child the guard may signal: not the agent CLI, not wick,
// and a recognisable build/test/browser/script tool.
func disposable(p agentProc) bool {
	if p.isAgent || neverTouch.MatchString(p.Cmdline) {
		return false
	}
	return childPattern.MatchString(p.Comm)
}

// pickChild is the disposable child holding the most memory (byMem) or
// using the most CPU since the last sample. Already-paused children are
// skipped: pausing them again frees nothing.
func pickChild(procs []agentProc, byMem bool, skip map[int]bool) (agentProc, bool) {
	var c []agentProc
	for _, p := range procs {
		if disposable(p) && !skip[p.PID] {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return agentProc{}, false
	}
	sort.SliceStable(c, func(i, j int) bool {
		if byMem {
			return c[i].RSSBytes > c[j].RSSBytes
		}
		if c[i].cpuTicks != c[j].cpuTicks {
			return c[i].cpuTicks > c[j].cpuTicks
		}
		return c[i].RSSBytes > c[j].RSSBytes
	})
	if !byMem && c[0].cpuTicks == 0 {
		return agentProc{}, false // nothing is actually burning CPU
	}
	return c[0], true
}

// pickScope is the heaviest agent scope — by memory, or by CPU used since
// the last sample — never wick's own and never one already frozen.
func (g *Guard) pickScope(scopes []Scope, procs []agentProc, byMem bool) (Scope, bool) {
	self := g.host.SelfScope()
	cpu := map[string]uint64{}
	for _, p := range procs {
		cpu[p.scope] += p.cpuTicks
	}
	var best Scope
	found := false
	for _, s := range scopes {
		if s.Name == self || g.frozen[s.Name] {
			continue
		}
		better := !found
		if found {
			if byMem {
				better = s.MemBytes > best.MemBytes
			} else {
				better = cpu[s.Name] > cpu[best.Name]
			}
		}
		if better {
			best, found = s, true
		}
	}
	return best, found
}

func gb(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }

func describe(p agentProc) string {
	cmd := p.Cmdline
	if f := strings.Fields(cmd); len(f) > 2 {
		cmd = strings.Join(f[:2], " ")
	}
	if cmd == "" {
		cmd = p.Comm
	}
	return cmd
}

func quotaLabel(pct int) string {
	if pct <= 0 {
		return "uncapped"
	}
	return fmt.Sprintf("%d%%", pct)
}
