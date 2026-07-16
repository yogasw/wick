// Package plugin is the host side of the connector plugin platform: it
// spawns connector subprocesses on demand, hands out live gRPC clients,
// and reaps idle ones.
package plugin

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type entry struct {
	client     *goplugin.Client
	conn       wickplugin.GRPCConn
	lastUsed   time.Time
	inflight   int
	reattached bool // attached to a debugger-run plugin, not a spawned child
}

// Manager owns connector plugin subprocesses keyed by connector Meta.Key.
type Manager struct {
	mu           sync.Mutex
	entries      map[string]*entry
	binaries     map[string]string
	idleTimeout  time.Duration
	maxProcs     int
	queueTimeout time.Duration
	warm         map[string]bool
	spawnFn      func(key string) (*entry, error)
	killFn       func(key string)
	now          func() time.Time
	stop         chan struct{}
	cond         *sync.Cond
	breakers     map[string]*breaker
	socketDir    string
}

// NewManager builds a Manager and starts the idle sweeper.
func NewManager(binaries map[string]string, idleTimeout time.Duration) *Manager {
	m := &Manager{
		entries:      map[string]*entry{},
		binaries:     binaries,
		idleTimeout:  idleTimeout,
		maxProcs:     envMaxProcs(),
		queueTimeout: envQueueTimeout(),
		warm:         envWarmSet(),
		now:          time.Now,
		stop:         make(chan struct{}),
		breakers:     map[string]*breaker{},
		socketDir:    RunDir(),
	}
	m.cond = sync.NewCond(&m.mu)
	m.spawnFn = m.spawn
	m.killFn = m.kill
	_ = os.MkdirAll(m.socketDir, 0o700)
	go m.sweepLoop()
	return m
}

func (m *Manager) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := m.now().Add(-m.idleTimeout)
	for key, e := range m.entries {
		if !m.warm[key] && e.inflight == 0 && e.lastUsed.Before(cutoff) {
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
	if m.cond != nil {
		m.cond.Broadcast() // wake any queued Client waiter so it exits on shutdown
	}
	for key := range m.entries {
		m.kill(key)
		delete(m.entries, key)
	}
}

// WarmUp eagerly spawns every warm connector that has a registered binary so
// it is hot before the first call. Failures are logged and skipped — boot must
// not abort. Call once at boot after Load wires the binaries.
func (m *Manager) WarmUp() {
	m.mu.Lock()
	keys := make([]string, 0, len(m.warm))
	for k := range m.warm {
		if _, ok := m.binaries[k]; ok {
			keys = append(keys, k)
		}
	}
	maxProcs := m.maxProcs
	m.mu.Unlock()
	if maxProcs > 0 && len(keys) >= maxProcs {
		log.Warn().Int("warm", len(keys)).Int("max_procs", maxProcs).
			Msg("connector plugin warm set >= max procs; non-warm spawns will queue")
	}
	for _, k := range keys {
		lease, err := m.Client(k)
		if err != nil {
			log.Warn().Str("connector", k).Err(err).Msg("connector plugin warm-up failed")
			continue
		}
		lease.Release()
	}
}

func (m *Manager) spawn(key string) (*entry, error) {
	// caller (Client) holds m.mu.
	clientCfg := &goplugin.ClientConfig{
		HandshakeConfig:  wickplugin.Handshake,
		VersionedPlugins: wickplugin.VersionedPlugins,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		AutoMTLS:         true,
		UnixSocketConfig: &goplugin.UnixSocketConfig{TempDir: m.socketDir},
	}
	// Debug: attach to a debugger-run plugin instead of spawning our own child,
	// but ONLY when ReadReattachConfig confirms it's reachable (it dials the
	// addr). A stale file from a stopped/relaunched dlv fails the dial and we
	// fall through to a normal spawn — this is what stops attach-to-zombie,
	// dispense mismatches, and circuit trips from desynced reattach files.
	// AutoMTLS off: the running plugin never got our client cert.
	reattached := false
	if rp := reattachPathFor(key); rp != "" {
		if rc, err := wickplugin.ReadReattachConfig(rp); err == nil {
			clientCfg.Reattach = rc
			clientCfg.AutoMTLS = false
			// Reattach skips version negotiation, so go-plugin never copies the
			// matching VersionedPlugins set into config.Plugins — the dispense
			// would then fail with "unknown plugin type". Set Plugins directly.
			clientCfg.Plugins = wickplugin.ReattachPluginSet()
			reattached = true
			log.Info().Str("connector", key).Str("reattach", rp).
				Int("pid", rc.Pid).Msg("attaching to debug plugin instead of spawning")
		} else {
			log.Debug().Str("connector", key).Str("reattach", rp).Err(err).
				Msg("no live debug plugin; spawning normally")
		}
	}
	if clientCfg.Reattach == nil {
		bin, ok := m.binaries[key]
		if !ok {
			return nil, fmt.Errorf("no plugin binary registered for %q", key)
		}
		clientCfg.Cmd = safeexec.Command(bin)
	}
	client := goplugin.NewClient(clientCfg)
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
	return &entry{client: client, conn: conn, lastUsed: m.now(), reattached: reattached}, nil
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
	// Debug takeover / release: keep the cached entry consistent with whether a
	// LIVE debugger currently owns this key. reattachPathFor + the dial in
	// ReadReattachConfig make "live" precise, so this flips cleanly both ways:
	//   spawned child + debugger now live      → drop child, re-spawn (attach)
	//   reattached + debugger gone/unreachable  → drop attach, re-spawn (child)
	// Without this, whichever entry got cached first serves forever — the exact
	// reason breakpoints never bound when the lab spawned before dlv started.
	if e != nil {
		live := reattachPathFor(key) != "" && func() bool {
			_, err := wickplugin.ReadReattachConfig(reattachPathFor(key))
			return err == nil
		}()
		if live != e.reattached {
			m.kill(key)
			delete(m.entries, key)
			e = nil
		}
	}
	if e == nil {
		if err := m.ensureSlotLocked(); err != nil {
			return nil, err
		}
		select {
		case <-m.stop:
			return nil, fmt.Errorf("connector plugin manager shutting down")
		default:
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
