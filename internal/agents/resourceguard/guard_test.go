package resourceguard

import (
	"math"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeHost is a scripted machine. Nothing here touches a real process.
type fakeHost struct {
	totalMB      int
	avail        int
	memFull      float64
	cpuSome      float64
	busyPct      float64 // per tick, turned into /proc/stat jiffies
	busy, total  uint64
	running      int
	scopes       []Scope
	procs        map[int]Proc
	signals      []string
	frozen       map[string]bool
	killedScopes []string
	quota        []int
	self         string
	// agentStep is the agents' CPU usec per tick; agentOK=false = unknown.
	agentOK    bool
	agentStep  uint64
	agentUsec  uint64
	outsideTop *Proc
}

func (f *fakeHost) MemAvailable() (int, int, bool) { return f.avail, f.totalMB, true }
func (f *fakeHost) PSI(r string) (float64, float64, bool) {
	if r == "memory" {
		return 0, f.memFull, true
	}
	return f.cpuSome, 0, true
}
func (f *fakeHost) CPUTimes() (uint64, uint64, bool) {
	f.total += 1000
	f.busy += uint64(f.busyPct * 10)
	return f.busy, f.total, true
}
func (f *fakeHost) ProcsRunning() int { return f.running }
func (f *fakeHost) NumCPU() int       { return 2 }
func (f *fakeHost) Scopes() []Scope   { return f.scopes }
func (f *fakeHost) Proc(pid int) (Proc, bool) {
	p, ok := f.procs[pid]
	return p, ok
}
func (f *fakeHost) SetSliceCPU(q, _, _ int) error { f.quota = append(f.quota, q); return nil }
func (f *fakeHost) Freeze(s string, v bool) error {
	if f.frozen == nil {
		f.frozen = map[string]bool{}
	}
	f.frozen[s] = v
	return nil
}
func (f *fakeHost) KillScope(s string) error { f.killedScopes = append(f.killedScopes, s); return nil }
func (f *fakeHost) Signal(pid int, sig syscall.Signal) error {
	f.signals = append(f.signals, sig.String()+":"+f.procs[pid].Comm)
	if sig == syscall.SIGKILL {
		delete(f.procs, pid)
	}
	return nil
}
func (f *fakeHost) SelfScope() string { return f.self }
func (f *fakeHost) AgentCPUUsec() (uint64, bool) {
	if !f.agentOK {
		return 0, false
	}
	f.agentUsec += f.agentStep
	return f.agentUsec, true
}
func (f *fakeHost) BusiestOutside(map[int]bool) (Proc, string, uint64, bool) {
	if f.outsideTop == nil {
		return Proc{}, "", 0, false
	}
	return *f.outsideTop, "bob", 700, true
}

// agentTree: one agent scope with the CLI (100), vitest (101, big), a
// go build (103, smaller) and a node MCP-ish helper (102); plus wick.
func agentTree() *fakeHost {
	return &fakeHost{
		totalMB: 4000, avail: 3000, self: "wick.scope",
		scopes: []Scope{
			{Name: "claude-agent-1-1.scope", MemBytes: 2 << 30, PIDs: []int{100, 101, 102, 103}},
			{Name: "wick.scope", MemBytes: 1 << 30, PIDs: []int{1}},
		},
		procs: map[int]Proc{
			1:   {PID: 1, PPID: 0, Comm: "support-tools", Cmdline: "support-tools daemon", RSSBytes: 900 << 20},
			100: {PID: 100, PPID: 50, Comm: "claude", Cmdline: "claude --resume x", RSSBytes: 400 << 20},
			101: {PID: 101, PPID: 100, Comm: "vitest", Cmdline: "node vitest run", RSSBytes: 900 << 20},
			102: {PID: 102, PPID: 100, Comm: "node", Cmdline: "node mcp.js", RSSBytes: 50 << 20},
			103: {PID: 103, PPID: 100, Comm: "go", Cmdline: "go test ./x", RSSBytes: 300 << 20},
		},
	}
}

type clock struct{ t time.Time }

func (c *clock) step(d time.Duration) time.Time { c.t = c.t.Add(d); return c.t }

func newTestGuard(h *fakeHost, cfg Config) (*Guard, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	g := New(h, func() Config { return cfg })
	g.now = func() time.Time { return c.t }
	return g, c
}

func enforce() Config {
	return Config{Enabled: true, Action: ActionKill, Interval: time.Second,
		SafePct: 80, HorizonSec: 20, CPUPSIMax: 60}
}

func ticks(g *Guard, c *clock, n int) {
	for i := 0; i < n; i++ {
		c.step(time.Second)
		g.Tick()
	}
}

func TestSlopeAndProjection(t *testing.T) {
	t0 := time.Unix(0, 0)
	var s []sample
	for i := 0; i < 10; i++ {
		s = append(s, sample{at: t0.Add(time.Duration(i) * time.Second), availMB: 1000 - 50*float64(i)})
	}
	if got := slopeMBps(s); math.Abs(got+50) > 0.01 {
		t.Fatalf("slope = %v, want -50", got)
	}
	if eta := secondsToExhaustion(550, -50); math.Abs(eta-11) > 0.01 {
		t.Fatalf("eta = %v, want 11", eta)
	}
	if !math.IsInf(secondsToExhaustion(500, 5), 1) {
		t.Fatal("rising memory must never project an exhaustion")
	}
	if slopeMBps(s[:2]) != 0 {
		t.Fatal("two samples are not a trend")
	}
	if s = trimWindow(s, t0.Add(9*time.Second), 5*time.Second); len(s) != 6 {
		t.Fatalf("trimmed to %d samples, want 6", len(s))
	}
}

// Memory near a hang: kill children one at a time, biggest resident
// first, re-measuring in between, and stop as soon as the host is under
// the safe line. The agent CLI and wick are never touched.
func TestMemoryKillsChildrenOneByOneUntilSafe(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.avail = 300 // 92.5% used
	ticks(g, c, 1)
	if len(h.signals) != 1 || h.signals[0] != "killed:vitest" {
		t.Fatalf("first action = %v, want SIGKILL to the biggest child (vitest)", h.signals)
	}
	if !g.HoldSpawns() {
		t.Fatal("spawns must be held while the host is near a hang")
	}
	ticks(g, c, 1) // inside the re-measure gap
	if len(h.signals) != 1 {
		t.Fatalf("acted again before re-measuring: %v", h.signals)
	}
	h.avail = 700 // 82.5%: better, still above 80
	ticks(g, c, 1)
	if len(h.signals) != 2 || h.signals[1] != "killed:go" {
		t.Fatalf("signals = %v, want the go build next", h.signals)
	}
	h.avail = 1200 // 70%: safe
	ticks(g, c, 5)
	if len(h.signals) != 2 {
		t.Fatalf("kept killing under the safe line: %v", h.signals)
	}
	last := g.History()[len(g.History())-1]
	if last.Kind != "resolved" {
		t.Fatalf("last event = %+v, want resolved", last)
	}
	if g.HoldSpawns() {
		t.Fatal("spawn hold not released once the host is safe")
	}
}

// With no disposable child left and the host still above the line, the
// heaviest agent scope goes — never wick's own, never by signalling the CLI.
func TestNoChildLeftKillsHeaviestScope(t *testing.T) {
	h := agentTree()
	h.scopes[0].PIDs = []int{100, 102}
	h.procs[102] = Proc{PID: 102, PPID: 100, Comm: "claude-helper", Cmdline: "claude helper"}
	g, c := newTestGuard(h, enforce())
	h.avail = 200
	ticks(g, c, 1)
	if len(h.killedScopes) != 1 || h.killedScopes[0] != "claude-agent-1-1.scope" {
		t.Fatalf("killed scopes = %v", h.killedScopes)
	}
	if len(h.signals) != 0 {
		t.Fatalf("the agent CLI was signalled directly: %v", h.signals)
	}
}

// Falling fast while above the safe line is a near hang even before 90%.
func TestFallingMemoryAboveSafeLineActs(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.avail = 1000 // 75%
	for i := 0; i < 4; i++ {
		h.avail -= 100 // ~-100 MB/s, reaching 85% used
		ticks(g, c, 1)
	}
	if len(h.signals) == 0 {
		t.Fatalf("no action while memory fell fast above the safe line: %+v", g.History())
	}
}

// Busy CPU with no queue is a build using idle CPU: leave it. Busy with
// a queue for 10s is a near hang: brief throttle, then kill the child
// that burned the most CPU since the last sample.
func TestCPUNeedsBusyAndQueueing(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	cfg.CPUQuotaPct = 140
	g, c := newTestGuard(h, cfg)
	burn := func() {
		p := h.procs[103]
		p.CPUTicks += 190
		h.procs[103] = p
	}
	h.busyPct = 100
	for i := 0; i < 15; i++ {
		burn()
		ticks(g, c, 1)
	}
	if len(h.signals) != 0 {
		t.Fatalf("busy CPU without a queue was treated as a hang: %v", h.signals)
	}
	h.cpuSome = 75
	for i := 0; i < 11; i++ {
		burn()
		ticks(g, c, 1)
	}
	if len(h.signals) == 0 || h.signals[0] != "killed:go" {
		t.Fatalf("signals = %v, want the CPU hog (go) killed, not the bigger vitest", h.signals)
	}
	if h.quota[len(h.quota)-1] != cpuThrottlePct {
		t.Fatalf("quota writes = %v, want a throttle", h.quota)
	}
	h.busyPct, h.cpuSome = 20, 5
	ticks(g, c, 31)
	if h.quota[len(h.quota)-1] != 140 {
		t.Fatalf("quota not restored after calm: %v", h.quota)
	}
}

// A starved wick (tick a second late) acts at once, no 10s wait.
func TestTickLagActsImmediately(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.busyPct = 85
	ticks(g, c, 1)
	p := h.procs[103]
	p.CPUTicks += 100
	h.procs[103] = p
	c.step(3 * time.Second) // the ticker fired 2s late
	g.Tick()
	if len(h.signals) != 1 || h.signals[0] != "killed:go" {
		t.Fatalf("signals = %v, want an immediate kill", h.signals)
	}
}

// measure records "would" events and touches nothing.
func TestMeasureModeTouchesNothing(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	cfg.Measure = true
	g, c := newTestGuard(h, cfg)
	h.avail = 100
	ticks(g, c, 12)
	if len(h.signals)+len(h.killedScopes)+len(h.quota) != 0 {
		t.Fatalf("measure mode acted: signals=%v killed=%v quota=%v", h.signals, h.killedScopes, h.quota)
	}
	var would bool
	for _, e := range g.History() {
		if e.Kind == "kill_child" && e.DryRun {
			would = true
		}
	}
	if !would {
		t.Fatalf("measure mode did not record what it would kill: %+v", g.History())
	}
}

// pause stops and freezes instead of killing, and resumes after calm.
func TestPauseActionStopsThenResumes(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	cfg.Action = ActionPause
	g, c := newTestGuard(h, cfg)
	h.avail = 300
	ticks(g, c, 9)
	for _, s := range h.signals {
		if s == "killed:vitest" || s == "killed:go" {
			t.Fatalf("pause action killed: %v", h.signals)
		}
	}
	if h.signals[0] != "stopped (signal):vitest" || !h.frozen["claude-agent-1-1.scope"] {
		t.Fatalf("signals=%v frozen=%v, want vitest stopped then the scope frozen", h.signals, h.frozen)
	}
	h.avail = 3000
	ticks(g, c, 31)
	resumed := false
	for _, s := range h.signals {
		resumed = resumed || s == "continued:vitest"
	}
	if h.frozen["claude-agent-1-1.scope"] || !resumed {
		t.Fatalf("not resumed after calm: signals=%v frozen=%v", h.signals, h.frozen)
	}
}

// A changed config quota is written on the next tick, once, and reported.
func TestQuotaAppliedLiveOnChange(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	c := &clock{t: time.Unix(0, 0)}
	g := New(h, func() Config { return cfg })
	g.now = func() time.Time { return c.t }
	n := 0
	g.OnQuotaApplied = func() { n++ }
	g.Tick()
	g.Tick()
	cfg.CPUQuotaPct = 140
	g.Tick()
	g.Tick()
	if len(h.quota) != 2 || h.quota[0] != 0 || h.quota[1] != 140 || n != 2 {
		t.Fatalf("quota writes = %v (notified %d), want [0 140] notified twice", h.quota, n)
	}
	cfg.Enabled = false
	cfg.CPUQuotaPct = 50
	g.Tick()
	if len(h.quota) != 2 {
		t.Fatal("a disabled guard must not write the slice")
	}
}

// A run-* unit an agent started outside agents.slice has no agent CLI:
// its root process is disposable, and its events carry no agent pid.
func TestDetachedRunUnitIsCovered(t *testing.T) {
	h := agentTree()
	h.scopes = []Scope{{Name: "app.slice/run-u7.service", Detached: true, PIDs: []int{200}}, h.scopes[1]}
	h.procs[200] = Proc{PID: 200, PPID: 1, Comm: "vite", Cmdline: "node vite build", RSSBytes: 1500 << 20}
	g, c := newTestGuard(h, enforce())
	h.avail = 300
	ticks(g, c, 1)
	if len(h.signals) != 1 || h.signals[0] != "killed:vite" {
		t.Fatalf("signals = %v, want the detached vite build killed", h.signals)
	}
	var ev Event
	for _, e := range g.History() {
		if e.Kind == "kill_child" {
			ev = e
		}
	}
	if ev.Scope != "app.slice/run-u7.service" || ev.AgentPID != 0 {
		t.Fatalf("event = %+v", ev)
	}
}

// Events from an agent scope name the agent CLI, so its session can be told.
func TestEventCarriesAgentPID(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.avail = 100
	ticks(g, c, 1)
	for _, e := range g.History() {
		if e.Kind == "kill_child" && e.AgentPID == 100 {
			return
		}
	}
	t.Fatalf("events = %+v, want a kill carrying agent pid 100", g.History())
}

func TestScopeDirRefusesEscapes(t *testing.T) {
	for _, bad := range []string{"", "..", "a/b", "app.slice/support-tools.service", "app.slice/run-x/../../y"} {
		if _, err := scopeDirIn("/u", "/u/agents.slice", bad); err == nil {
			t.Fatalf("scope %q accepted", bad)
		}
	}
	if d, err := scopeDirIn("/u", "/u/agents.slice", "app.slice/run-u1.service"); err != nil || d != "/u/app.slice/run-u1.service" {
		t.Fatalf("run unit = %q %v", d, err)
	}
	if d, err := scopeDirIn("/u", "/u/agents.slice", "claude-agent-1-1.scope"); err != nil || d != "/u/agents.slice/claude-agent-1-1.scope" {
		t.Fatalf("agent scope = %q %v", d, err)
	}
}

func TestParsers(t *testing.T) {
	some, full, ok := parsePSI("some avg10=12.50 avg60=1.00 avg300=0.10 total=1\nfull avg10=3.25 avg60=0 avg300=0 total=1\n")
	if !ok || some != 12.5 || full != 3.25 {
		t.Fatalf("psi = %v %v %v", some, full, ok)
	}
	stat := "cpu  100 5 50 800 20 3 2 10 0 0\ncpu0 1 2 3 4 5 6 7 8 0 0\nprocs_running 7\n"
	busy, total, ok := parseCPUTimes(stat)
	if !ok || busy != 170 || total != 990 {
		t.Fatalf("cpu times = %d %d %v, want 170 990", busy, total, ok)
	}
	if n := parseProcsRunning(stat); n != 7 {
		t.Fatalf("procs_running = %d", n)
	}
}

// hotCPU drives a CPU near-hang for n ticks with agents using step usec
// of CPU per tick (a tick is 1000 busy jiffies = 10,000,000 usec).
func hotCPU(h *fakeHost, g *Guard, c *clock, step uint64, n int) {
	h.agentOK, h.agentStep = true, step
	h.busyPct, h.cpuSome = 100, 75
	for i := 0; i < n; i++ {
		p := h.procs[103]
		p.CPUTicks += 190
		h.procs[103] = p
		ticks(g, c, 1)
	}
}

// A build outside wick pinning the CPU: agents use 10% of it, so the
// guard leaves them running, does not hold spawns, and says who it is.
func TestOutsideCPULoadLeavesAgentsAlone(t *testing.T) {
	h := agentTree()
	h.outsideTop = &Proc{PID: 900, Comm: "compile"}
	g, c := newTestGuard(h, enforce())
	hotCPU(h, g, c, 1_000_000, 15)
	if len(h.signals) != 0 || len(h.killedScopes) != 0 || len(h.frozen) != 0 {
		t.Fatalf("agents touched for an outside load: signals=%v scopes=%v frozen=%v", h.signals, h.killedScopes, h.frozen)
	}
	for _, q := range h.quota {
		if q == cpuThrottlePct {
			t.Fatalf("agents throttled for an outside load: %v", h.quota)
		}
	}
	if g.HoldSpawns() {
		t.Fatal("spawns held for an outside CPU load")
	}
	var outside []Event
	for _, e := range g.History() {
		if e.Kind == "outside_busy" {
			outside = append(outside, e)
		}
	}
	if len(outside) != 1 || !strings.Contains(outside[0].Detail, "host busy from outside wick: bob/compile 70% CPU") {
		t.Fatalf("outside events = %+v", outside)
	}
}

// Agents using 90% of the busy CPU: the old behaviour, the hog goes.
func TestAgentCPULoadStillActs(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	hotCPU(h, g, c, 9_000_000, 12)
	if len(h.signals) == 0 || h.signals[0] != "killed:go" {
		t.Fatalf("signals = %v, want the agent's go build killed", h.signals)
	}
}

// Memory nearly gone, but agents hold ~13% of it: nothing is killed, yet
// spawns wait because memory really is running out.
func TestOutsideMemoryLoadHoldsSpawnsOnly(t *testing.T) {
	h := agentTree()
	h.totalMB, h.avail = 16000, 300
	g, c := newTestGuard(h, enforce())
	ticks(g, c, 3)
	if len(h.signals) != 0 || len(h.killedScopes) != 0 {
		t.Fatalf("agents touched for an outside memory load: %v %v", h.signals, h.killedScopes)
	}
	if !g.HoldSpawns() {
		t.Fatal("spawns not held with memory nearly gone")
	}
}
