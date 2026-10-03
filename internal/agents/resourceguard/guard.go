// Package resourceguard is wick's fast watchdog for the agent tree. It
// samples memory and CPU every second, projects where memory is heading,
// and climbs a ladder of actions — kill a runaway child, freeze its
// agent, kill the agent; throttle the slice, pause the CPU hog, kill it —
// before the machine stops answering.
//
// The kernel OOM killer and the slice limits are the backstop; they act
// at the cliff edge, by which time a small host has often been swapping
// or thrashing for a minute. This acts on the trend.
package resourceguard

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// Actions, from mildest to strongest. Action config caps the ladder.
const (
	ActionOff   = "off"
	ActionLog   = "log"
	ActionPause = "pause"
	ActionKill  = "kill"
)

// Ladder timings. Fixed rather than configurable: they encode how fast a
// small host goes from "busy" to "unreachable", and a knob nobody can
// reason about is worse than a sound default.
const (
	trendWindow     = 10 * time.Second
	killCooldown    = 3 * time.Second
	freezeAfter     = 6 * time.Second  // critical this long despite child kills
	scopeKillAfter  = 10 * time.Second // frozen and still critical
	thawAfterCalm   = 15 * time.Second
	cpuHotFor       = 10 * time.Second
	cpuStopAfter    = 20 * time.Second
	cpuResumeCalm   = 30 * time.Second
	cpuRestoreCalm  = 60 * time.Second
	cpuThrottle1Pct = 100
	cpuThrottle2Pct = 70
	psiMemFullMax   = 20
	tickLagMax      = time.Second
	historyMax      = 100
)

// childPattern is what counts as a disposable child: build tools, test
// runners, browsers, scripts. The agent CLI and wick are never matched.
var childPattern = regexp.MustCompile(`^(node|esbuild|vite|vitest|go|compile|link|asm|cgo|gopls|chrome|chromium|headless_shell|python3?|npm|npx|pnpm|yarn|bun|deno|tsc|cargo|rustc|gcc|cc1|cc1plus|ld|make|java|playwright)$`)

// neverTouch matches command lines the guard must never act on.
var neverTouch = regexp.MustCompile(`(?i)\b(claude|codex|support-tools|wick|opencode|gemini)\b`)

// Config is read fresh on every tick so a change in the UI applies
// without a restart.
type Config struct {
	Enabled     bool   // memory_guard_mode == enforce on Linux
	Action      string // off|log|pause|kill
	Interval    time.Duration
	HorizonSec  int     // act when memory is projected to run out sooner
	MinFreeMB   int     // hard floor
	CPUPSIMax   float64 // cpu some avg10 above this counts as hot
	CPUQuotaPct int     // configured slice quota, restored after a throttle
	CPUWeight   int
	TasksMax    int
}

// Event is one guard action, kept for the Resources page and pushed to
// OnEvent.
type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // kill_child|freeze|thaw|kill_scope|throttle|restore|stop_child|cont_child|log
	Scope  string    `json:"scope,omitempty"`
	PID    int       `json:"pid,omitempty"`
	Target string    `json:"target,omitempty"`
	// AgentPID is the agent CLI that owns the scope, so the caller can
	// tell that agent's session what happened. 0 = unknown (a run-* unit).
	AgentPID int    `json:"agent_pid,omitempty"`
	Detail   string `json:"detail"`
}

// Guard is the watchdog. Zero value is not usable; use New.
type Guard struct {
	host    Host
	load    func() Config
	OnEvent func(Event)
	now     func() time.Time

	mu      sync.Mutex
	history []Event
	hold    bool // spawn gate, read by HoldSpawns

	samples []sample
	prevRSS map[int]uint64
	prevCPU map[int]uint64
	prevAt  time.Time
	prevMem map[string]uint64

	memCritSince time.Time
	lastKill     time.Time
	frozen       string
	frozenAt     time.Time
	calmSince    time.Time

	cpuHotSince  time.Time
	cpuCalmSince time.Time
	throttlePct  int // 0 = not throttled
	stopped      map[int]time.Time
	resumed      map[int]bool // pids resumed once; hot again → kill
	appliedQuota *[3]int
	lastTick     time.Time
	logOnly      bool
	scopeAgent   map[string]int // scope → agent CLI pid, from the last sample
}

// New builds a guard over host, reading its config through load.
func New(host Host, load func() Config) *Guard {
	return &Guard{
		host: host, load: load, now: time.Now,
		prevRSS: map[int]uint64{}, prevCPU: map[int]uint64{}, prevMem: map[string]uint64{},
		stopped: map[int]time.Time{}, resumed: map[int]bool{},
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
			interval = time.Second
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

// HoldSpawns reports whether a new agent should wait: memory is critical
// or heading there. The spawn gate that a static free-memory floor cannot
// be — a host losing 200 MB/s with 900 MB left is not fine.
func (g *Guard) HoldSpawns() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hold
}

func (g *Guard) emit(e Event) {
	e.At = g.now()
	if e.AgentPID == 0 && e.Scope != "" {
		e.AgentPID = g.scopeAgent[e.Scope]
	}
	if g.logOnly {
		// Action "log": nothing was done, so the record must not say it was.
		e.Detail = "[log only, no action taken] " + e.Detail
	}
	g.mu.Lock()
	g.history = append(g.history, e)
	if len(g.history) > historyMax {
		g.history = g.history[len(g.history)-historyMax:]
	}
	g.mu.Unlock()
	log.Warn().Str("component", "resourceguard").Str("kind", e.Kind).Str("scope", e.Scope).
		Int("pid", e.PID).Str("target", e.Target).Msg(e.Detail)
	if g.OnEvent != nil {
		g.OnEvent(e)
	}
}

// Tick runs one sample-decide-act pass. Exported for tests.
func (g *Guard) Tick() {
	cfg := g.load()
	now := g.now()
	// The daemon's own responsiveness: a tick that fires a second late
	// means wick itself is starved, which is one rung up on its own.
	lagging := false
	if !g.lastTick.IsZero() && cfg.Interval > 0 && now.Sub(g.lastTick)-cfg.Interval > tickLagMax {
		lagging = true
	}
	g.lastTick = now
	if !cfg.Enabled || cfg.Action == ActionOff || cfg.Action == "" {
		g.setHold(false)
		return
	}
	g.logOnly = cfg.Action == ActionLog
	g.applyQuota(cfg)

	scopes := g.host.Scopes()
	procs := g.readProcs(scopes)
	g.memoryPass(cfg, now, scopes, procs)
	g.cpuPass(cfg, now, procs, lagging)

	// Growth baselines for the next tick.
	g.prevAt = now
	g.prevRSS, g.prevCPU = map[int]uint64{}, map[int]uint64{}
	for _, p := range procs {
		g.prevRSS[p.PID] = p.RSSBytes
		g.prevCPU[p.PID] = p.CPUTicks
	}
	g.prevMem = map[string]uint64{}
	for _, s := range scopes {
		g.prevMem[s.Name] = s.MemBytes
	}
}

func (g *Guard) setHold(v bool) {
	g.mu.Lock()
	g.hold = v
	g.mu.Unlock()
}

// applyQuota writes the configured slice CPU controls when they change —
// the "live" in live quota. Skipped while a throttle is in force; the
// restore step writes the configured value back.
func (g *Guard) applyQuota(cfg Config) {
	if g.throttlePct != 0 {
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
}

// agentProc is a process with its scope and per-second growth.
type agentProc struct {
	Proc
	scope    string
	isAgent  bool // the agent CLI itself (the scope's root process)
	rssRate  float64
	cpuTicks uint64 // ticks used since the previous sample
}

func (g *Guard) readProcs(scopes []Scope) []agentProc {
	self := g.host.SelfScope()
	dt := g.now().Sub(g.prevAt).Seconds()
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
			if prev, ok := g.prevRSS[pid]; ok && dt > 0 {
				ap.rssRate = (float64(p.RSSBytes) - float64(prev)) / dt
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

// pickGrowing is the disposable child growing fastest, falling back to
// the largest when nothing has a rate yet.
func pickGrowing(procs []agentProc) (agentProc, bool) {
	var c []agentProc
	for _, p := range procs {
		if disposable(p) {
			c = append(c, p)
		}
	}
	if len(c) == 0 {
		return agentProc{}, false
	}
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].rssRate != c[j].rssRate {
			return c[i].rssRate > c[j].rssRate
		}
		return c[i].RSSBytes > c[j].RSSBytes
	})
	return c[0], true
}

// pickCPUHog is the disposable child that used the most CPU since the
// last sample.
func pickCPUHog(procs []agentProc) (agentProc, bool) {
	var best agentProc
	found := false
	for _, p := range procs {
		if disposable(p) && p.cpuTicks > 0 && (!found || p.cpuTicks > best.cpuTicks) {
			best, found = p, true
		}
	}
	return best, found
}

// pickScope is the agent scope growing fastest (largest as tie-break).
func (g *Guard) pickScope(scopes []Scope) (Scope, bool) {
	self := g.host.SelfScope()
	var best Scope
	bestRate := math.Inf(-1)
	found := false
	for _, s := range scopes {
		if s.Name == self {
			continue
		}
		rate := float64(s.MemBytes) - float64(g.prevMem[s.Name])
		if !found || rate > bestRate || (rate == bestRate && s.MemBytes > best.MemBytes) {
			best, bestRate, found = s, rate, true
		}
	}
	return best, found
}

func mb(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }

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

func (g *Guard) memoryPass(cfg Config, now time.Time, scopes []Scope, procs []agentProc) {
	avail, _, ok := g.host.MemAvailable()
	if !ok {
		return
	}
	g.samples = trimWindow(append(g.samples, sample{at: now, availMB: float64(avail)}), now, trendWindow)
	slope := slopeMBps(g.samples)
	eta := secondsToExhaustion(float64(avail), slope)
	_, full10, _ := g.host.PSI("memory")

	horizon := float64(cfg.HorizonSec)
	floor := cfg.MinFreeMB
	reason := ""
	switch {
	case floor > 0 && avail < floor:
		reason = fmt.Sprintf("%d MB free, below the %d MB floor", avail, floor)
	case horizon > 0 && eta < horizon && (floor <= 0 || avail < 2*floor):
		reason = fmt.Sprintf("%d MB free, falling %.0f MB/s — out in ~%.0fs", avail, -slope, eta)
	case full10 > psiMemFullMax:
		reason = fmt.Sprintf("memory pressure full avg10 %.0f%%", full10)
	}
	// The spawn gate opens a horizon early: hold new agents while memory
	// is heading for the floor even before the ladder needs to act.
	g.setHold(reason != "" || (horizon > 0 && eta < 2*horizon && (floor <= 0 || avail < 3*floor)))

	if reason == "" {
		g.memCritSince = time.Time{}
		if g.calmSince.IsZero() {
			g.calmSince = now
		}
		if g.frozen != "" && now.Sub(g.calmSince) >= thawAfterCalm {
			if g.act(cfg, ActionPause) {
				_ = g.host.Freeze(g.frozen, false)
			}
			g.emit(Event{Kind: "thaw", Scope: g.frozen, Detail: "memory calm again; resumed " + g.frozen})
			g.frozen = ""
		}
		return
	}
	g.calmSince = time.Time{}
	if g.memCritSince.IsZero() {
		g.memCritSince = now
	}
	crit := now.Sub(g.memCritSince)

	// Rung 3: the frozen agent is still the problem.
	if g.frozen != "" && now.Sub(g.frozenAt) >= scopeKillAfter {
		if g.act(cfg, ActionKill) {
			_ = g.host.KillScope(g.frozen)
		}
		g.emit(Event{Kind: "kill_scope", Scope: g.frozen,
			Detail: fmt.Sprintf("wick stopped agent %s to keep the host alive (%s); the session resumes on its next message", g.frozen, reason)})
		g.frozen = ""
		g.lastKill = now
		return
	}
	// Rung 1: a runaway child.
	if now.Sub(g.lastKill) >= killCooldown {
		if p, ok := pickGrowing(procs); ok && (crit < freezeAfter || g.frozen != "") {
			if g.act(cfg, ActionKill) {
				_ = g.host.Signal(p.PID, syscall.SIGKILL)
			} else if g.act(cfg, ActionPause) {
				_ = g.host.Signal(p.PID, syscall.SIGSTOP)
				g.stopped[p.PID] = now
			}
			g.emit(Event{Kind: "kill_child", Scope: p.scope, PID: p.PID, Target: describe(p),
				Detail: fmt.Sprintf("wick stopped `%s` (%s, %+.0f MB/s) to keep the host alive: %s",
					describe(p), mb(p.RSSBytes), p.rssRate/(1<<20), reason)})
			g.lastKill = now
			return
		}
	}
	// Rung 2: still critical after child kills, or nothing to kill.
	if g.frozen == "" && crit >= freezeAfter {
		if s, ok := g.pickScope(scopes); ok {
			if g.act(cfg, ActionPause) {
				_ = g.host.Freeze(s.Name, true)
			}
			g.frozen, g.frozenAt = s.Name, now
			g.emit(Event{Kind: "freeze", Scope: s.Name,
				Detail: fmt.Sprintf("wick paused agent %s (%s) to keep the host alive: %s", s.Name, mb(s.MemBytes), reason)})
		}
	}
}

// act reports whether the configured action reaches the rung `need`.
// log only records; pause stops short of killing.
func (g *Guard) act(cfg Config, need string) bool {
	rank := map[string]int{ActionLog: 1, ActionPause: 2, ActionKill: 3}
	return rank[cfg.Action] >= rank[need]
}

func (g *Guard) cpuPass(cfg Config, now time.Time, procs []agentProc, lagging bool) {
	some10, _, _ := g.host.PSI("cpu")
	max := cfg.CPUPSIMax
	if max <= 0 {
		max = 90
	}
	n := g.host.NumCPU()
	hot := some10 > max || (n > 0 && g.host.Load1()/float64(n) > 2)
	if !hot && !lagging {
		g.cpuHotSince = time.Time{}
		if g.cpuCalmSince.IsZero() {
			g.cpuCalmSince = now
		}
		calm := now.Sub(g.cpuCalmSince)
		for pid, at := range g.stopped {
			if _, alive := g.host.Proc(pid); !alive {
				delete(g.stopped, pid)
				continue
			}
			if calm >= cpuResumeCalm || now.Sub(at) >= 2*cpuResumeCalm {
				_ = g.host.Signal(pid, syscall.SIGCONT)
				delete(g.stopped, pid)
				g.resumed[pid] = true
				g.emit(Event{Kind: "cont_child", PID: pid, Detail: fmt.Sprintf("CPU calm; resumed pid %d", pid)})
			}
		}
		if g.throttlePct != 0 && calm >= cpuRestoreCalm {
			_ = g.host.SetSliceCPU(cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax)
			g.throttlePct = 0
			q := [3]int{cfg.CPUQuotaPct, cfg.CPUWeight, cfg.TasksMax}
			g.appliedQuota = &q
			g.emit(Event{Kind: "restore", Detail: fmt.Sprintf("CPU calm for %s; agent CPU quota back to %s", cpuRestoreCalm, quotaLabel(cfg.CPUQuotaPct))})
		}
		return
	}
	g.cpuCalmSince = time.Time{}
	if g.cpuHotSince.IsZero() {
		g.cpuHotSince = now
		if lagging {
			// A starved daemon skips the wait: one rung up immediately.
			g.cpuHotSince = now.Add(-cpuHotFor)
		}
	}
	hotFor := now.Sub(g.cpuHotSince)
	reason := fmt.Sprintf("CPU pressure %.0f%% for %s", some10, hotFor.Round(time.Second))
	if lagging {
		reason += ", wick itself responding slowly"
	}

	// Rung 1: throttle the slice, 100% then 70%.
	if hotFor >= cpuHotFor {
		next := 0
		switch {
		case g.throttlePct == 0:
			next = cpuThrottle1Pct
		case g.throttlePct == cpuThrottle1Pct && hotFor >= cpuHotFor+cpuHotFor:
			next = cpuThrottle2Pct
		}
		if cfg.CPUQuotaPct > 0 && next > cfg.CPUQuotaPct && g.throttlePct == 0 {
			next = cpuThrottle2Pct // already capped tighter than rung 1
		}
		if next != 0 {
			if g.act(cfg, ActionPause) {
				_ = g.host.SetSliceCPU(next, cfg.CPUWeight, cfg.TasksMax)
			}
			g.throttlePct = next
			g.emit(Event{Kind: "throttle", Detail: fmt.Sprintf("agent CPU capped at %d%% for now: %s", next, reason)})
		}
	}
	// Rung 2 and 3: pause the hog; a hog that comes back hot is killed.
	if hotFor >= cpuStopAfter && now.Sub(g.lastKill) >= killCooldown {
		p, ok := pickCPUHog(procs)
		if !ok {
			return
		}
		if _, already := g.stopped[p.PID]; already {
			return
		}
		if g.resumed[p.PID] {
			if g.act(cfg, ActionKill) {
				_ = g.host.Signal(p.PID, syscall.SIGKILL)
			}
			delete(g.resumed, p.PID)
			g.emit(Event{Kind: "kill_child", Scope: p.scope, PID: p.PID, Target: describe(p),
				Detail: fmt.Sprintf("wick stopped `%s` — it took the CPU again after a pause (%s); re-run it when the host is quieter", describe(p), reason)})
		} else {
			if g.act(cfg, ActionPause) {
				_ = g.host.Signal(p.PID, syscall.SIGSTOP)
				g.stopped[p.PID] = now
			}
			g.emit(Event{Kind: "stop_child", Scope: p.scope, PID: p.PID, Target: describe(p),
				Detail: fmt.Sprintf("wick paused `%s` to keep the host responsive (%s); it resumes once the CPU is calm", describe(p), reason)})
		}
		g.lastKill = now
	}
}

func quotaLabel(pct int) string {
	if pct <= 0 {
		return "uncapped"
	}
	return fmt.Sprintf("%d%%", pct)
}
