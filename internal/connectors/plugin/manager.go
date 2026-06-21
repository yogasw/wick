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
}

// Manager owns connector plugin subprocesses keyed by connector Meta.Key.
type Manager struct {
	mu          sync.Mutex
	entries     map[string]*entry
	binaries    map[string]string
	idleTimeout time.Duration
	spawnFn     func(key string) (*entry, error)
	killFn      func(key string)
	now         func() time.Time
	stop        chan struct{}
}

// NewManager builds a Manager and starts the idle sweeper.
func NewManager(binaries map[string]string, idleTimeout time.Duration) *Manager {
	m := &Manager{
		entries:     map[string]*entry{},
		binaries:    binaries,
		idleTimeout: idleTimeout,
		now:         time.Now,
		stop:        make(chan struct{}),
	}
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
		if e.lastUsed.Before(cutoff) {
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
func (m *Manager) SetBinary(key, path string) {
	m.mu.Lock()
	m.binaries[key] = path
	m.mu.Unlock()
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
	conn, err := m.Client(key)
	if err != nil {
		return "", "", err
	}
	return conn.ResolveIdentity(ctx, token)
}

// Client returns a live gRPC connection for key, spawning the subprocess on
// first use (lazy) and re-spawning if a previous process died.
func (m *Manager) Client(key string) (wickplugin.GRPCConn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[key]
	if e != nil && e.client != nil && e.client.Exited() {
		m.kill(key)
		delete(m.entries, key)
		e = nil
	}
	if e == nil {
		spawned, err := m.spawnFn(key)
		if err != nil {
			return nil, err
		}
		m.entries[key] = spawned
		e = spawned
	}
	e.lastUsed = m.now()
	return e.conn, nil
}
