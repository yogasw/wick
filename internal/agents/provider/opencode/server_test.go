package opencode

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// fakeStarter hands out fake servers and records what happened to them.
type fakeStarter struct {
	mu      sync.Mutex
	started int
	killed  int
	fail    error
	last    *fakeServer
}

type fakeServer struct {
	done chan struct{}
	once sync.Once
}

func (f *fakeServer) crash() { f.once.Do(func() { close(f.done) }) }

func (f *fakeStarter) start(ctx context.Context, spec serverSpec, password string) (*serverHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.started++
	fs := &fakeServer{done: make(chan struct{})}
	f.last = fs
	return &serverHandle{url: "http://fake", password: password, pid: 1000 + f.started, done: fs.done, kill: func() {
		f.mu.Lock()
		f.killed++
		f.mu.Unlock()
		fs.crash()
	}}, nil
}

func (f *fakeStarter) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.killed
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestManager(fs *fakeStarter) (*manager, *fakeClock) {
	m := newManager(fs.start)
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	m.Now = clk.now
	m.Every = time.Hour // the tests drive reap() themselves
	return m, clk
}

func spec(idle time.Duration) serverSpec {
	return serverSpec{instance: "oc", bin: "/bin/opencode", env: []string{"XDG_DATA_HOME=/a"}, idle: idle}
}

func TestManagerLazyStartAndReuse(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	if n, _ := fs.counts(); n != 0 {
		t.Fatalf("started before any turn: %d", n)
	}
	l1, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	l2, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if l1.s.h != l2.s.h {
		t.Fatal("same spec got two servers")
	}
	if n, _ := fs.counts(); n != 1 {
		t.Fatalf("started %d servers, want 1", n)
	}
	// A different env (other account / other config) is another server.
	other := spec(time.Minute)
	other.env = []string{"XDG_DATA_HOME=/b"}
	l3, err := m.acquire(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if l3.s.h == l1.s.h {
		t.Fatal("different env shared a server")
	}
	l1.release()
	l2.release()
	l3.release()
}

func TestManagerIdleKill(t *testing.T) {
	fs := &fakeStarter{}
	m, clk := newTestManager(fs)
	l, err := m.acquire(context.Background(), spec(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// A running turn is never reaped, however long it takes.
	clk.add(time.Hour)
	if got := m.reap(); len(got) != 0 {
		t.Fatalf("reaped a server with a turn in flight: %v", got)
	}
	l.release()
	clk.add(9 * time.Minute)
	if got := m.reap(); len(got) != 0 {
		t.Fatalf("reaped before the idle window: %v", got)
	}
	clk.add(time.Minute)
	if got := m.reap(); len(got) != 1 {
		t.Fatalf("idle server not reaped: %v", got)
	}
	if _, k := fs.counts(); k != 1 {
		t.Fatalf("killed %d, want 1", k)
	}
	// The next turn starts a fresh one.
	l, err = m.acquire(context.Background(), spec(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	l.release()
	if n, _ := fs.counts(); n != 2 {
		t.Fatalf("started %d, want 2 after reap", n)
	}
}

// The reaper cannot be switched off: no idle window means the default.
func TestManagerIdleZeroMeansDefault(t *testing.T) {
	for _, idle := range []time.Duration{0, -time.Minute} {
		fs := &fakeStarter{}
		m, clk := newTestManager(fs)
		l, err := m.acquire(context.Background(), spec(idle))
		if err != nil {
			t.Fatal(err)
		}
		l.release()
		clk.add(DefaultServerIdle - time.Second)
		if got := m.reap(); len(got) != 0 {
			t.Fatalf("idle=%v: reaped early", idle)
		}
		clk.add(time.Second)
		if got := m.reap(); len(got) != 1 {
			t.Fatalf("idle=%v: not reaped at the default window", idle)
		}
	}
}

// The reaper runs by itself once the manager is used.
func TestManagerReapLoopRuns(t *testing.T) {
	fs := &fakeStarter{}
	m := newManager(fs.start)
	m.Every = 10 * time.Millisecond
	l, err := m.acquire(context.Background(), spec(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	l.release()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, k := fs.counts(); k == 1 {
			m.shutdown()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background reaper never killed the idle server")
}

func TestManagerRestartAfterCrash(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	l, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	l.release()
	fs.last.crash()
	l, err = m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer l.release()
	if n, _ := fs.counts(); n != 2 {
		t.Fatalf("started %d, want a restart after the crash", n)
	}
	if l.s.dead() {
		t.Fatal("lease on a dead server")
	}
}

func TestManagerFailedStartNotCached(t *testing.T) {
	fs := &fakeStarter{fail: errors.New("boom")}
	m, _ := newTestManager(fs)
	if _, err := m.acquire(context.Background(), spec(time.Minute)); err == nil {
		t.Fatal("want start error")
	}
	fs.mu.Lock()
	fs.fail = nil
	fs.mu.Unlock()
	l, err := m.acquire(context.Background(), spec(time.Minute))
	if err != nil {
		t.Fatalf("failed start was cached: %v", err)
	}
	l.release()
}

func TestManagerSlotsQueue(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	sp := spec(time.Minute)
	sp.turns = 1
	a, _ := m.acquire(context.Background(), sp)
	b, _ := m.acquire(context.Background(), sp)
	if err := a.waitSlot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.waitSlot(ctx); err == nil {
		t.Fatal("second turn got a slot past the limit")
	}
	a.release()
	if err := b.waitSlot(context.Background()); err != nil {
		t.Fatalf("slot not freed by release: %v", err)
	}
	b.release()
}

func TestManagerShutdownKillsAll(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	l, _ := m.acquire(context.Background(), spec(time.Minute))
	other := spec(time.Minute)
	other.instance = "oc2"
	l2, _ := m.acquire(context.Background(), other)
	m.shutdown()
	if _, k := fs.counts(); k != 2 {
		t.Fatalf("shutdown killed %d, want 2", k)
	}
	l.release()
	l2.release()
	if _, err := m.acquire(context.Background(), spec(time.Minute)); err == nil {
		t.Fatal("acquire after shutdown started a server")
	}
}

// Settings change (other env) → the old server finishes its turn, then dies.
func TestManagerStaleServerStopsWhenIdle(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	old, _ := m.acquire(context.Background(), spec(time.Hour))
	changed := spec(time.Hour)
	changed.env = []string{"XDG_DATA_HOME=/a", "OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1"}
	nw, _ := m.acquire(context.Background(), changed)
	if _, k := fs.counts(); k != 0 {
		t.Fatal("old server killed while its turn was running")
	}
	old.release()
	if _, k := fs.counts(); k != 1 {
		t.Fatal("stale server not stopped after its last turn")
	}
	nw.release()
	if _, k := fs.counts(); k != 1 {
		t.Fatal("current server stopped with the stale one")
	}
}

// Server mode switched off → idle servers stop now, busy ones after.
func TestManagerRetire(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	idle, _ := m.acquire(context.Background(), spec(time.Hour))
	idle.release()
	other := spec(time.Hour)
	other.env = []string{"X=1"}
	busy, _ := m.acquire(context.Background(), other) // also retires the first (other env)
	if _, k := fs.counts(); k != 1 {
		t.Fatalf("idle server of the old env should have stopped")
	}
	m.retire("oc")
	if _, k := fs.counts(); k != 1 {
		t.Fatal("retire killed a server with a running turn")
	}
	busy.release()
	if _, k := fs.counts(); k != 2 {
		t.Fatal("retired server not stopped after its turn")
	}
	if m.Len() != 0 {
		t.Fatalf("servers left: %d", m.Len())
	}
}

// A serve that dies before listening (port taken) is retried on a new
// port inside the same start, up to serveAttempts.
func TestStartServeRetriesPort(t *testing.T) {
	prev := serveOnce
	t.Cleanup(func() { serveOnce = prev })
	calls := 0
	serveOnce = func(ctx context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		calls++
		if calls < serveAttempts {
			return nil, errNotListening
		}
		return &serverHandle{url: "http://ok", pid: 7, kill: func() {}, done: make(chan struct{})}, nil
	}
	h, err := startServe(context.Background(), spec(time.Minute), "pw")
	if err != nil || h == nil || calls != serveAttempts {
		t.Fatalf("h=%v err=%v calls=%d", h, err, calls)
	}
	calls = -10 // every attempt fails
	if _, err := startServe(context.Background(), spec(time.Minute), "pw"); !errors.Is(err, errNotListening) {
		t.Fatalf("want errNotListening after %d attempts, got %v", serveAttempts, err)
	}
	// Other failures are not retried.
	calls = 0
	serveOnce = func(context.Context, serverSpec, string) (*serverHandle, error) {
		calls++
		return nil, errors.New("health timeout")
	}
	if _, err := startServe(context.Background(), spec(time.Minute), "pw"); err == nil || calls != 1 {
		t.Fatalf("non-port error retried: calls=%d err=%v", calls, err)
	}
}

// Two account folders of one instance each keep their own server: a turn
// on a2 must not mark main's server stale (they used to kill each other on
// every alternation), while a config change inside one folder still does.
func TestManagerAccountFoldersKeepOwnServers(t *testing.T) {
	fs := &fakeStarter{}
	m, _ := newTestManager(fs)
	main := spec(time.Hour)
	main.dir = "/a"
	a2 := spec(time.Hour)
	a2.dir, a2.env = "/a/accounts/a2", []string{"XDG_DATA_HOME=/a/accounts/a2"}
	for i := 0; i < 3; i++ {
		for _, sp := range []serverSpec{main, a2} {
			l, err := m.acquire(context.Background(), sp)
			if err != nil {
				t.Fatal(err)
			}
			l.release()
		}
	}
	if st, k := fs.counts(); st != 2 || k != 0 {
		t.Fatalf("alternating accounts: started=%d killed=%d, want 2/0", st, k)
	}
	changed := a2
	changed.env = append([]string{"OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1"}, a2.env...)
	l, _ := m.acquire(context.Background(), changed)
	l.release()
	if st, k := fs.counts(); st != 3 || k != 1 {
		t.Fatalf("config change in a2: started=%d killed=%d, want 3/1 (only a2's old server)", st, k)
	}
}

// A shut-down manager refuses every later lease: tests that share the
// package-global one must swap in a fresh manager (useFreshServers)
// instead of shutting the global down, or every later server-mode test in
// the same process fails to spawn.
func TestManagerShutdownIsTerminalAndFreshServersIsolates(t *testing.T) {
	fs := &fakeStarter{}
	for i := 0; i < 2; i++ { // two tests in a row, same process
		t.Run("", func(t *testing.T) {
			useFreshServers(t, fs.start)
			l, err := servers.acquire(context.Background(), spec(time.Hour))
			if err != nil {
				t.Fatalf("acquire after a previous test: %v", err)
			}
			l.release()
		})
	}
	m, _ := newTestManager(fs)
	m.shutdown()
	if _, err := m.acquire(context.Background(), spec(time.Hour)); err == nil {
		t.Fatal("a shut-down manager must refuse leases")
	}
}

// useFreshServers gives the test its own server manager and restores the
// package one afterwards (shutting down only the test's).
func useFreshServers(t *testing.T, start startFunc) {
	t.Helper()
	prev := servers
	m := newManager(start)
	m.Every = time.Hour
	servers = m
	t.Cleanup(func() { m.shutdown(); servers = prev })
}

// An idle `opencode serve` yields to another instance's spawn
// (provider.YieldIdleServers via the registered yielder); one with a turn
// leased, or on the spawning instance, does not.
func TestIdleServeYieldsToOtherSpawn(t *testing.T) {
	fs := &fakeStarter{}
	useFreshServers(t, fs.start)
	idle, err := servers.acquire(context.Background(), serverSpec{instance: "oc-a", bin: "/bin/opencode", env: []string{"XDG_DATA_HOME=/a"}, idle: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	idle.release()
	busy, err := servers.acquire(context.Background(), serverSpec{instance: "oc-b", bin: "/bin/opencode", env: []string{"XDG_DATA_HOME=/b"}, idle: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if n := provider.YieldIdleServers("s-claude", ""); n != 1 {
		t.Fatalf("stopped %d, want only the idle one", n)
	}
	busy.release()
	// The spawning instance's own server stays.
	if n := provider.YieldIdleServers("s-x", "oc-b"); n != 0 {
		t.Fatalf("own instance's server stopped (%d)", n)
	}
	if n := provider.YieldIdleServers("s-x", "claude-work"); n != 1 {
		t.Fatalf("idle oc-b not stopped for another spawn (%d)", n)
	}
}
