// Package plugin is the host side of the connector plugin platform: it
// spawns connector subprocesses on demand, hands out live gRPC clients,
// and reaps idle ones.
package plugin

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type entry struct {
	client   *goplugin.Client
	conn     wickplugin.GRPCConn
	lastUsed time.Time
	inflight int
}

// Manager owns connector plugin subprocesses keyed by connector Meta.Key.
type Manager struct {
	mu           sync.Mutex
	entries      map[string]*entry
	binaries     map[string]string
	idleTimeout  time.Duration
	maxProcs     int
	queueTimeout time.Duration
	spawnFn      func(key string) (*entry, error)
	killFn       func(key string)
	now          func() time.Time
	stop         chan struct{}
	cond         *sync.Cond
	breakers     map[string]*breaker
}

// NewManager builds a Manager and starts the idle sweeper.
func NewManager(binaries map[string]string, idleTimeout time.Duration) *Manager {
	m := &Manager{
		entries:      map[string]*entry{},
		binaries:     binaries,
		idleTimeout:  idleTimeout,
		maxProcs:     envMaxProcs(),
		queueTimeout: envQueueTimeout(),
		now:          time.Now,
		stop:         make(chan struct{}),
		breakers:     map[string]*breaker{},
	}
	m.cond = sync.NewCond(&m.mu)
	m.spawnFn = m.spawn
	m.killFn = m.kill
	go m.sweepLoop()
	return m
}

func (m *Manager) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := m.now().Add(-m.idleTimeout)
	for key, e := range m.entries {
		if e.inflight == 0 && e.lastUsed.Before(cutoff) {
			// kill before delete: kill reads the entry from m.entries.
			m.killFn(key)
			delete(m.entries, key)
		}
	}
}

func (m *Manager) sweepLoop() {
	t := time.NewTicker(m.idleTimeout)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.sweep()
		case <-m.stop:
			return
		}
	}
}

func (m *Manager) kill(key string) {
	if e := m.entries[key]; e != nil && e.client != nil {
		e.client.Kill()
	}
}

// KillAll reaps every subprocess (call on app shutdown). It is safe to call
// more than once: the stop channel is closed at most once.
func (m *Manager) KillAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.stop:
		// already stopped
	default:
		close(m.stop)
	}
	for key := range m.entries {
		m.kill(key)
		delete(m.entries, key)
	}
}

func (m *Manager) spawn(key string) (*entry, error) {
	// caller (Client) holds m.mu.
	bin, ok := m.binaries[key]
	if !ok {
		return nil, fmt.Errorf("no plugin binary registered for %q", key)
	}
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  wickplugin.Handshake,
		VersionedPlugins: wickplugin.VersionedPlugins,
		Cmd:              exec.Command(bin),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		AutoMTLS:         true,
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin handshake %q: %w", key, err)
	}
	raw, err := rpc.Dispense(wickplugin.PluginName)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispense %q: %w", key, err)
	}
	conn, ok := raw.(wickplugin.GRPCConn)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin %q returned unexpected client type", key)
	}
	return &entry{client: client, conn: conn, lastUsed: m.now()}, nil
}

// IsPlugin reports whether key is served by a plugin subprocess.
func (m *Manager) IsPlugin(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.binaries[key]
	return ok
}

// SetBinary registers or updates the on-disk binary path for a connector key.
// If a subprocess is already running for this key it is killed so the next
// Client call spawns the new binary.
func (m *Manager) SetBinary(key, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.binaries[key] = path
	if e := m.entries[key]; e != nil {
		m.kill(key)
		delete(m.entries, key)
	}
}

// RemoveBinary drops a connector key and kills its running subprocess (if any).
func (m *Manager) RemoveBinary(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.binaries, key)
	if e := m.entries[key]; e != nil {
		m.kill(key)
		delete(m.entries, key)
	}
}

// ResolveIdentity spawns-if-needed and asks the plugin to resolve an OAuth
// token's owner.
func (m *Manager) ResolveIdentity(ctx context.Context, key, token string) (string, string, error) {
	lease, err := m.Client(key)
	if err != nil {
		return "", "", err
	}
	defer lease.Release()
	return lease.Conn.ResolveIdentity(ctx, token)
}

// ensureInit lazily wires fields that struct-literal test Managers omit.
// Caller holds m.mu.
func (m *Manager) ensureInit() {
	if m.cond == nil {
		m.cond = sync.NewCond(&m.mu)
	}
}

// Client returns a lease on a live gRPC connection for key, spawning the
// subprocess on first use (lazy) and re-spawning if a previous process died.
// Release the lease when the call completes.
func (m *Manager) Client(key string) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()
	if err := m.breakerOpenLocked(key); err != nil {
		return nil, err
	}
	e := m.entries[key]
	if e != nil && e.client != nil && e.client.Exited() {
		m.kill(key)
		delete(m.entries, key)
		e = nil
	}
	if e == nil {
		if err := m.ensureSlotLocked(); err != nil {
			return nil, err
		}
		spawned, err := m.spawnFn(key)
		if err != nil {
			m.recordFailureLocked(key)
			return nil, err
		}
		m.resetBreakerLocked(key)
		m.entries[key] = spawned
		e = spawned
	}
	e.lastUsed = m.now()
	e.inflight++
	return m.leaseLocked(e), nil
}
