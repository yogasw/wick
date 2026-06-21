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
