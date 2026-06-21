package plugin

import (
	"context"
	"sync"
	"testing"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// stubConn is a no-op GRPCConn for pool/lease tests.
type stubConn struct{}

func (stubConn) Execute(_ context.Context, _ wickplugin.ExecCall) ([]byte, error) { return nil, nil }
func (stubConn) Schema(context.Context) ([]byte, error)                           { return nil, nil }
func (stubConn) ResolveIdentity(context.Context, string) (string, string, error) {
	return "", "", nil
}

func newTestManager() *Manager {
	m := &Manager{
		entries:  map[string]*entry{},
		binaries: map[string]string{},
		now:      func() time.Time { return time.Unix(0, 0) },
		stop:     make(chan struct{}),
	}
	m.cond = sync.NewCond(&m.mu)
	m.killFn = func(string) {}
	return m
}

func TestClientLeaseTracksInflight(t *testing.T) {
	m := newTestManager()
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }

	lease, err := m.Client("x")
	if err != nil {
		t.Fatal(err)
	}
	if m.entries["x"].inflight != 1 {
		t.Fatalf("inflight should be 1 while leased, got %d", m.entries["x"].inflight)
	}
	lease.Release()
	if m.entries["x"].inflight != 0 {
		t.Fatalf("inflight should be 0 after release, got %d", m.entries["x"].inflight)
	}
}

func TestCapEvictsLRUIdle(t *testing.T) {
	m := newTestManager()
	m.maxProcs = 2
	killed := map[string]bool{}
	m.killFn = func(k string) { killed[k] = true }
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }

	m.now = func() time.Time { return time.Unix(10, 0) }
	la, _ := m.Client("a")
	la.Release()
	m.now = func() time.Time { return time.Unix(20, 0) }
	lb, _ := m.Client("b")
	lb.Release()

	m.now = func() time.Time { return time.Unix(30, 0) }
	lc, err := m.Client("c")
	if err != nil {
		t.Fatal(err)
	}
	lc.Release()
	if !killed["a"] {
		t.Fatal("oldest idle 'a' should be evicted")
	}
	if _, ok := m.entries["a"]; ok {
		t.Fatal("'a' should be gone")
	}
	if _, ok := m.entries["c"]; !ok {
		t.Fatal("'c' should be spawned")
	}
}

func TestQueueWaitsThenSucceeds(t *testing.T) {
	m := newTestManager()
	m.now = time.Now
	m.maxProcs = 1
	m.queueTimeout = 2 * time.Second
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }

	la, _ := m.Client("a")
	done := make(chan error, 1)
	go func() {
		lb, err := m.Client("b")
		if lb != nil {
			lb.Release()
		}
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	la.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued call should succeed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued call did not complete")
	}
}

func TestQueueTimesOut(t *testing.T) {
	m := newTestManager()
	m.now = time.Now
	m.maxProcs = 1
	m.queueTimeout = 80 * time.Millisecond
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }

	la, _ := m.Client("a")
	defer la.Release()
	if _, err := m.Client("b"); err == nil {
		t.Fatal("expected timeout error when pool is full and all busy")
	}
}

func TestClientRejectsAfterShutdown(t *testing.T) {
	m := newTestManager()
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }
	m.KillAll()
	if _, err := m.Client("x"); err == nil {
		t.Fatal("Client must reject spawning after KillAll")
	}
}

func TestKillAllWakesQueuedWaiter(t *testing.T) {
	m := newTestManager()
	m.now = time.Now
	m.maxProcs = 1
	m.queueTimeout = 10 * time.Second // long: only KillAll's broadcast can wake it promptly
	m.spawnFn = func(string) (*entry, error) { return &entry{conn: stubConn{}}, nil }

	la, _ := m.Client("a") // busy, holds the only slot
	_ = la
	done := make(chan error, 1)
	go func() {
		_, err := m.Client("b") // queues (all busy)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	m.KillAll()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("queued waiter should get a shutdown error, not spawn after KillAll")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("KillAll did not promptly wake the queued waiter")
	}
}
