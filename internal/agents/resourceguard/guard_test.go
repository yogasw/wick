package resourceguard

import (
	"math"
	"syscall"
	"testing"
	"time"
)

// fakeHost is a scripted machine. Nothing here touches a real process.
type fakeHost struct {
	avail        int
	memFull      float64
	cpuSome      float64
	load         float64
	scopes       []Scope
	procs        map[int]Proc
	signals      []string
	frozen       map[string]bool
	killedScopes []string
	quota        []int // every SetSliceCPU quota written
	self         string
}

func (f *fakeHost) MemAvailable() (int, int, bool) { return f.avail, 3600, true }
func (f *fakeHost) PSI(r string) (float64, float64, bool) {
	if r == "memory" {
		return 0, f.memFull, true
	}
	return f.cpuSome, 0, true
}
func (f *fakeHost) Load1() float64  { return f.load }
func (f *fakeHost) NumCPU() int     { return 2 }
func (f *fakeHost) Scopes() []Scope { return f.scopes }
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
	return nil
}
func (f *fakeHost) SelfScope() string { return f.self }

// agentTree is one agent scope: the CLI (pid 100), a vitest child (101)
// and a small node MCP-ish helper (102); plus wick's own scope.
func agentTree() *fakeHost {
	return &fakeHost{
		avail: 3000,
		self:  "wick.scope",
		scopes: []Scope{
			{Name: "claude-agent-1-1.scope", MemBytes: 1 << 30, PIDs: []int{100, 101, 102}},
			{Name: "wick.scope", MemBytes: 1 << 30, PIDs: []int{1}},
		},
		procs: map[int]Proc{
			1:   {PID: 1, PPID: 0, Comm: "support-tools", Cmdline: "support-tools daemon"},
			100: {PID: 100, PPID: 50, Comm: "claude", Cmdline: "claude --resume x"},
			101: {PID: 101, PPID: 100, Comm: "vitest", Cmdline: "node vitest run", RSSBytes: 500 << 20},
			102: {PID: 102, PPID: 100, Comm: "node", Cmdline: "node mcp.js", RSSBytes: 50 << 20},
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
		HorizonSec: 20, MinFreeMB: 300, CPUPSIMax: 90}
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
	s = trimWindow(s, t0.Add(9*time.Second), 5*time.Second)
	if len(s) != 6 {
		t.Fatalf("trimmed to %d samples, want 6", len(s))
	}
}

// Memory falling fast kills the fastest-growing CHILD — never the agent
// CLI, never wick — and only once per cooldown.
func TestFallingMemoryKillsTheGrowingChildOnly(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.avail = 900 // −150 MB/s from here is ~6s from empty
	for i := 0; i < 4; i++ {
		h.avail -= 150
		p := h.procs[101]
		p.RSSBytes += 150 << 20
		h.procs[101] = p
		c.step(time.Second)
		g.Tick()
	}
	if len(h.signals) != 1 || h.signals[0] != "killed:vitest" {
		t.Fatalf("signals = %v, want one SIGKILL to vitest", h.signals)
	}
	if !g.HoldSpawns() {
		t.Fatal("spawns must be held while memory is heading for the floor")
	}
	ev := g.History()
	if len(ev) != 1 || ev[0].Kind != "kill_child" || ev[0].PID != 101 {
		t.Fatalf("history = %+v", ev)
	}
	// Cooldown: another tick 1s later must not kill again.
	h.avail -= 150
	c.step(time.Second)
	g.Tick()
	if len(h.signals) != 1 {
		t.Fatalf("killed again inside the cooldown: %v", h.signals)
	}
}

// With nothing disposable left, the ladder freezes the agent, thaws it
// when memory is calm, and kills a frozen agent that stays critical.
func TestFreezeThenThawOrKillScope(t *testing.T) {
	h := agentTree()
	delete(h.procs, 101)
	h.scopes[0].PIDs = []int{100, 102}
	h.procs[102] = Proc{PID: 102, PPID: 100, Comm: "claude-helper", Cmdline: "claude helper"}
	g, c := newTestGuard(h, enforce())
	h.avail = 200 // under the floor
	for i := 0; i < 7; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if !h.frozen["claude-agent-1-1.scope"] {
		t.Fatalf("agent not frozen after %s critical: %+v", freezeAfter, g.History())
	}
	if h.frozen["wick.scope"] {
		t.Fatal("wick's own scope must never be frozen")
	}

	// Calm for thawAfterCalm → thawed.
	h.avail = 3000
	for i := 0; i < 16; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if h.frozen["claude-agent-1-1.scope"] {
		t.Fatal("agent still frozen after memory calmed down")
	}

	// Critical again and it stays critical while frozen → scope killed.
	h.avail = 200
	for i := 0; i < 20; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if len(h.killedScopes) != 1 || h.killedScopes[0] != "claude-agent-1-1.scope" {
		t.Fatalf("killed scopes = %v", h.killedScopes)
	}
	if len(h.signals) != 0 {
		t.Fatalf("the agent CLI was signalled directly: %v", h.signals)
	}
}

// log records what it would do and touches nothing.
func TestLogActionTouchesNothing(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	cfg.Action = ActionLog
	g, c := newTestGuard(h, cfg)
	h.avail = 100
	for i := 0; i < 12; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if len(h.signals)+len(h.killedScopes) != 0 || h.frozen["claude-agent-1-1.scope"] {
		t.Fatalf("log mode acted: signals=%v killed=%v frozen=%v", h.signals, h.killedScopes, h.frozen)
	}
	if len(g.History()) == 0 {
		t.Fatal("log mode must still record")
	}
}

// CPU: throttle the slice, pause the hog, resume it when calm, kill it if
// it takes the CPU again, and restore the configured quota after 60s calm.
func TestCPULadderThrottlePauseKillRestore(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	cfg.CPUQuotaPct = 140
	g, c := newTestGuard(h, cfg)
	burn := func() {
		p := h.procs[101]
		p.CPUTicks += 180
		h.procs[101] = p
	}

	g.Tick() // applies the configured quota live
	if len(h.quota) != 1 || h.quota[0] != 140 {
		t.Fatalf("configured quota not applied: %v", h.quota)
	}
	h.cpuSome = 97
	for i := 0; i < 21; i++ {
		burn()
		c.step(time.Second)
		g.Tick()
	}
	if got := h.quota[len(h.quota)-1]; got != cpuThrottle2Pct {
		t.Fatalf("quota after 20s hot = %d, want %d (history %v)", got, cpuThrottle2Pct, h.quota)
	}
	if len(h.signals) != 1 || h.signals[0] != "stopped (signal):vitest" {
		t.Fatalf("signals = %v, want SIGSTOP to vitest", h.signals)
	}

	h.cpuSome = 10
	for i := 0; i < 31; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if h.signals[len(h.signals)-1] != "continued:vitest" {
		t.Fatalf("hog not resumed after calm: %v", h.signals)
	}

	h.cpuSome = 97
	for i := 0; i < 21; i++ {
		burn()
		c.step(time.Second)
		g.Tick()
	}
	if h.signals[len(h.signals)-1] != "killed:vitest" {
		t.Fatalf("hog that came back hot was not killed: %v", h.signals)
	}

	h.cpuSome = 5
	for i := 0; i < 61; i++ {
		c.step(time.Second)
		g.Tick()
	}
	if got := h.quota[len(h.quota)-1]; got != 140 {
		t.Fatalf("quota not restored to config after calm: %v", h.quota)
	}
}

// A changed config quota is written on the next tick, once.
func TestQuotaAppliedLiveOnChange(t *testing.T) {
	h := agentTree()
	cfg := enforce()
	c := &clock{t: time.Unix(0, 0)}
	g := New(h, func() Config { return cfg })
	g.now = func() time.Time { return c.t }
	g.Tick()
	g.Tick()
	cfg.CPUQuotaPct = 140
	g.Tick()
	g.Tick()
	if len(h.quota) != 2 || h.quota[0] != 0 || h.quota[1] != 140 {
		t.Fatalf("quota writes = %v, want [0 140]", h.quota)
	}
	cfg.Enabled = false
	cfg.CPUQuotaPct = 50
	g.Tick()
	if len(h.quota) != 2 {
		t.Fatal("a disabled guard must not write the slice")
	}
}

func TestParsers(t *testing.T) {
	some, full, ok := parsePSI("some avg10=12.50 avg60=1.00 avg300=0.10 total=1\nfull avg10=3.25 avg60=0 avg300=0 total=1\n")
	if !ok || some != 12.5 || full != 3.25 {
		t.Fatalf("psi = %v %v %v", some, full, ok)
	}
	p, ok := parseStat("4242 (node (vitest)) R 4000 1 1 0 -1 0 0 0 0 0 70 30 0 0 20 0 1 0 1 100 2560 0")
	if !ok || p.PID != 4242 || p.PPID != 4000 || p.Comm != "node (vitest)" || p.CPUTicks != 100 || p.RSSBytes == 0 {
		t.Fatalf("stat = %+v %v", p, ok)
	}
	if cpuMax(140) != "140000 100000" || cpuMax(0) != "max 100000" {
		t.Fatalf("cpuMax wrong: %q %q", cpuMax(140), cpuMax(0))
	}
}

// A run-* unit an agent started outside agents.slice has no agent CLI:
// its root process is disposable, and its events carry no agent pid.
func TestDetachedRunUnitIsCovered(t *testing.T) {
	h := agentTree()
	h.scopes = append(h.scopes, Scope{Name: "app.slice/run-u7.service", Detached: true, PIDs: []int{200}})
	h.procs[200] = Proc{PID: 200, PPID: 1, Comm: "go", Cmdline: "go test ./...", RSSBytes: 900 << 20}
	g, c := newTestGuard(h, enforce())
	h.avail = 900
	for i := 0; i < 4; i++ {
		h.avail -= 150
		p := h.procs[200]
		p.RSSBytes += 300 << 20
		h.procs[200] = p
		c.step(time.Second)
		g.Tick()
	}
	if len(h.signals) != 1 || h.signals[0] != "killed:go" {
		t.Fatalf("signals = %v, want the detached go build killed", h.signals)
	}
	ev := g.History()
	if ev[0].Scope != "app.slice/run-u7.service" || ev[0].AgentPID != 0 {
		t.Fatalf("event = %+v", ev[0])
	}
}

// Events from an agent scope name the agent CLI, so its session can be told.
func TestEventCarriesAgentPID(t *testing.T) {
	h := agentTree()
	g, c := newTestGuard(h, enforce())
	h.avail = 100
	c.step(time.Second)
	g.Tick()
	ev := g.History()
	if len(ev) == 0 || ev[0].AgentPID != 100 {
		t.Fatalf("events = %+v, want agent pid 100", ev)
	}
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
