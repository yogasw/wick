package cliserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeHandle struct {
	pid  int
	done chan struct{}
	once sync.Once
	kill func()
}

func (h *fakeHandle) Pid() int              { return h.pid }
func (h *fakeHandle) Done() <-chan struct{} { return h.done }
func (h *fakeHandle) Kill()                 { h.kill() }
func (h *fakeHandle) crash()                { h.once.Do(func() { close(h.done) }) }

type starter struct {
	mu      sync.Mutex
	started int
	killed  int
	stopped int
	fail    error
	last    *fakeHandle
}

func (f *starter) start(context.Context) (*fakeHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.started++
	h := &fakeHandle{pid: 1000 + f.started, done: make(chan struct{})}
	h.kill = func() {
		f.mu.Lock()
		f.killed++
		f.mu.Unlock()
		h.crash()
	}
	f.last = h
	return h, nil
}

func (f *starter) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.killed
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newTest(f *starter) (*Manager[*fakeHandle], *clock) {
	m := New[*fakeHandle]("test", 2)
	clk := &clock{t: time.Unix(1_000_000, 0)}
	m.Now = clk.now
	m.Every = time.Hour
	m.OnStop = func(string, *fakeHandle) { f.mu.Lock(); f.stopped++; f.mu.Unlock() }
	return m, clk
}

func sp(key string, idle time.Duration) Spec { return Spec{Instance: "i", Key: key, Idle: idle} }

func TestLazyReuseAndKeys(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	if n, _ := f.counts(); n != 0 {
		t.Fatal("started before a turn")
	}
	a, _ := m.Acquire(context.Background(), sp("k", time.Minute), f.start)
	b, _ := m.Acquire(context.Background(), sp("k", time.Minute), f.start)
	if a.H != b.H || !a.Fresh || b.Fresh {
		t.Fatalf("same key: shared=%v freshA=%v freshB=%v", a.H == b.H, a.Fresh, b.Fresh)
	}
	c, _ := m.Acquire(context.Background(), Spec{Instance: "j", Key: "other"}, f.start)
	if c.H == a.H {
		t.Fatal("different key shared a server")
	}
	a.Release()
	a.Release() // no-op
	b.Release()
	c.Release()
	if h, ok := m.Live("k"); !ok || h != a.H {
		t.Fatal("Live lost the idle server")
	}
}

func TestIdleReapAndZeroIdleDefault(t *testing.T) {
	for _, idle := range []time.Duration{0, -time.Minute, 10 * time.Minute} {
		f := &starter{}
		m, clk := newTest(f)
		l, _ := m.Acquire(context.Background(), sp("k", idle), f.start)
		clk.add(time.Hour)
		if got := m.Reap(); len(got) != 0 {
			t.Fatalf("idle=%v: reaped a busy server", idle)
		}
		l.Release()
		clk.add(DefaultIdle - time.Second)
		if got := m.Reap(); len(got) != 0 {
			t.Fatalf("idle=%v: reaped early", idle)
		}
		clk.add(time.Second)
		if got := m.Reap(); len(got) != 1 {
			t.Fatalf("idle=%v: not reaped", idle)
		}
		if f.stopped != 1 {
			t.Fatalf("OnStop ran %d times", f.stopped)
		}
	}
}

func TestReapLoopRuns(t *testing.T) {
	f := &starter{}
	m := New[*fakeHandle]("test", 1)
	m.Every = 10 * time.Millisecond
	l, _ := m.Acquire(context.Background(), sp("k", time.Millisecond), f.start)
	l.Release()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, k := f.counts(); k == 1 {
			m.Shutdown()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background reaper never ran")
}

func TestCrashRestartsAndFailedStartNotCached(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	l, _ := m.Acquire(context.Background(), sp("k", time.Minute), f.start)
	l.Release()
	f.last.crash()
	if _, ok := m.Live("k"); ok {
		t.Fatal("dead server reported live")
	}
	l, err := m.Acquire(context.Background(), sp("k", time.Minute), f.start)
	if err != nil || l.Dead() || !l.Fresh {
		t.Fatalf("no restart after crash: err=%v", err)
	}
	l.Release()
	if n, _ := f.counts(); n != 2 {
		t.Fatalf("started %d", n)
	}

	f2 := &starter{fail: errors.New("boom")}
	m2, _ := newTest(f2)
	if _, err := m2.Acquire(context.Background(), sp("k", time.Minute), f2.start); err == nil {
		t.Fatal("want start error")
	}
	f2.fail = nil
	l, err = m2.Acquire(context.Background(), sp("k", time.Minute), f2.start)
	if err != nil {
		t.Fatalf("failed start was cached: %v", err)
	}
	l.Release()
}

func TestSlotsLimit(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	s := sp("k", time.Minute)
	s.Turns = 1
	a, _ := m.Acquire(context.Background(), s, f.start)
	b, _ := m.Acquire(context.Background(), s, f.start)
	if err := a.WaitSlot(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if b.WaitSlot(ctx) == nil {
		t.Fatal("slot past the limit")
	}
	a.Release()
	if err := b.WaitSlot(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.Release()
}

func TestStaleRetireAndMaxAge(t *testing.T) {
	f := &starter{}
	m, clk := newTest(f)
	old, _ := m.Acquire(context.Background(), sp("a", time.Hour), f.start)
	nw, _ := m.Acquire(context.Background(), sp("b", time.Hour), f.start) // same instance, new key
	if _, k := f.counts(); k != 0 {
		t.Fatal("busy stale server killed")
	}
	old.Release()
	if _, k := f.counts(); k != 1 {
		t.Fatal("stale server not stopped after its last turn")
	}
	m.Retire("i")
	if _, k := f.counts(); k != 1 {
		t.Fatal("retire killed a busy server")
	}
	nw.Release()
	if _, k := f.counts(); k != 2 || m.Len() != 0 {
		t.Fatalf("retired server not stopped: killed=%d len=%d", k, m.Len())
	}

	s := sp("c", time.Hour)
	s.MaxAge = time.Hour
	l, _ := m.Acquire(context.Background(), s, f.start)
	clk.add(time.Hour)
	l2, _ := m.Acquire(context.Background(), s, f.start)
	if l2.H == l.H {
		t.Fatal("aged server reused")
	}
	l.Release()
	if _, k := f.counts(); k != 3 {
		t.Fatal("aged server not stopped after its turn")
	}
	l2.Release()

	// Discard / RetireKey: the lease's server is not handed out again.
	l3, _ := m.Acquire(context.Background(), s, f.start)
	l3.Discard()
	l3.Release()
	if _, ok := m.Live("c"); ok {
		t.Fatal("discarded server still live")
	}
	l4, _ := m.Acquire(context.Background(), s, f.start)
	l4.Release()
	m.RetireKey("c")
	if m.Len() != 0 {
		t.Fatal("RetireKey left the server")
	}
}

func TestShutdown(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	a, _ := m.Acquire(context.Background(), sp("a", time.Minute), f.start)
	b, _ := m.Acquire(context.Background(), Spec{Instance: "j", Key: "b"}, f.start)
	m.Shutdown()
	if _, k := f.counts(); k != 2 {
		t.Fatalf("shutdown killed %d", k)
	}
	a.Release()
	b.Release()
	if _, err := m.Acquire(context.Background(), sp("a", time.Minute), f.start); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("acquire after shutdown: %v", err)
	}
}

// Servers in different groups of one instance never retire each other;
// Retire(instance) still reaches all of them.
func TestGroupsDoNotSweepEachOther(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	a, _ := m.Acquire(context.Background(), Spec{Instance: "i", Group: "i/s1", Key: "s1"}, f.start)
	a.Release()
	b, _ := m.Acquire(context.Background(), Spec{Instance: "i", Group: "i/s2", Key: "s2"}, f.start)
	b.Release()
	if _, k := f.counts(); k != 0 || m.Len() != 2 {
		t.Fatalf("session servers swept each other: killed=%d len=%d", k, m.Len())
	}
	m.Retire("i")
	if _, k := f.counts(); k != 2 {
		t.Fatalf("retire reached %d of 2", k)
	}
}

// LiveInGroup finds any up server of the group, whatever its key, and
// skips stale and dead ones.
func TestLiveInGroup(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	if _, ok := m.LiveInGroup("i/g"); ok {
		t.Fatal("empty manager reported a server")
	}
	a, _ := m.Acquire(context.Background(), Spec{Instance: "i", Group: "i/g", Key: "k1"}, f.start)
	a.Release()
	if h, ok := m.LiveInGroup("i/g"); !ok || h != a.H {
		t.Fatal("live server not found by group")
	}
	if _, ok := m.LiveInGroup("i/other"); ok {
		t.Fatal("other group matched")
	}
	a.H.crash()
	if _, ok := m.LiveInGroup("i/g"); ok {
		t.Fatal("dead server returned")
	}
	b, _ := m.Acquire(context.Background(), Spec{Instance: "i", Group: "i/g", Key: "k2"}, f.start)
	m.Retire("i")
	if _, ok := m.LiveInGroup("i/g"); ok {
		t.Fatal("stale server returned")
	}
	b.Release()
}

// RetireGroups stops only the servers its match picks: another
// instance's server of the same session goes, everything else stays.
func TestRetireGroups(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	for _, s := range []Spec{
		{Instance: "a", Group: "a/s1", Key: "a1"},
		{Instance: "b", Group: "b/s1", Key: "b1"},
		{Instance: "a", Group: "a/s2", Key: "a2"},
	} {
		l, _ := m.Acquire(context.Background(), s, f.start)
		l.Release()
	}
	m.RetireGroups(func(instance, group string) bool { return instance != "b" && group == instance+"/s1" })
	if _, k := f.counts(); k != 1 || m.Len() != 2 {
		t.Fatalf("killed=%d len=%d, want 1 and 2", k, m.Len())
	}
	if _, ok := m.LiveInGroup("a/s1"); ok {
		t.Fatal("a/s1 survived")
	}
}

// RetireIdle stops idle servers only: one holding a lease (a turn running
// or queued) stays, and so does whatever keep protects.
func TestRetireIdle(t *testing.T) {
	f := &starter{}
	m, _ := newTest(f)
	idle, _ := m.Acquire(context.Background(), Spec{Instance: "a", Group: "a/s1", Key: "a1"}, f.start)
	idle.Release()
	busy, _ := m.Acquire(context.Background(), Spec{Instance: "b", Group: "b/s2", Key: "b2"}, f.start)
	own, _ := m.Acquire(context.Background(), Spec{Instance: "c", Group: "c/s3", Key: "c3"}, f.start)
	own.Release()
	n := m.RetireIdle(func(instance, group string) bool { return group == "c/s3" })
	if n != 1 {
		t.Fatalf("stopped %d, want 1", n)
	}
	if _, ok := m.LiveInGroup("a/s1"); ok {
		t.Fatal("idle server survived")
	}
	if _, ok := m.LiveInGroup("b/s2"); !ok {
		t.Fatal("busy server stopped")
	}
	if _, ok := m.LiveInGroup("c/s3"); !ok {
		t.Fatal("kept server stopped")
	}
	busy.Release()
	if n := m.RetireIdle(nil); n != 2 {
		t.Fatalf("after release: stopped %d, want 2", n)
	}
}
