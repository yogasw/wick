package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Lease is a borrowed plugin connection. Release MUST be called exactly once
// (the adapter does so via defer) so the Manager can account for in-flight
// calls and free the subprocess for eviction.
type Lease struct {
	Conn    wickplugin.GRPCConn
	release func()
}

// Release returns the lease to the Manager. Safe to call on a nil-release lease.
func (l *Lease) Release() {
	if l != nil && l.release != nil {
		l.release()
	}
}

const (
	defaultMaxProcs     = 8
	defaultQueueTimeout = 10 * time.Second
)

func envMaxProcs() int {
	if v := os.Getenv("WICK_PLUGIN_MAX_PROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxProcs
}

func envQueueTimeout() time.Duration {
	if v := os.Getenv("WICK_PLUGIN_QUEUE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultQueueTimeout
}

func envWarmSet() map[string]bool {
	out := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("WICK_PLUGIN_WARM"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = true
		}
	}
	return out
}

// reattachPathFor returns the debug reattach-file path configured for a plugin
// key, or "" when none is set. Honoured, most-specific first:
//
//	WICK_PLUGIN_REATTACH=<key>=<path>[,<key>=<path>...]  — explicit map
//	WICK_DEBUG_PLUGIN=<key>                              — conventional path
//	                    bin/plugin-<key>.reattach.json under the repo root
//
// Only the PATH is resolved here; liveness (is the debugger actually running?)
// is verified by wickplugin.ReadReattachConfig via a socket dial, so a stale
// file for a stopped/relaunched debugger cleanly falls back to a normal spawn.
func reattachPathFor(key string) string {
	for _, pair := range strings.Split(os.Getenv("WICK_PLUGIN_REATTACH"), ",") {
		k, v, ok := strings.Cut(pair, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	if strings.TrimSpace(os.Getenv("WICK_DEBUG_PLUGIN")) == key {
		return filepath.Join(devRepoRoot(), "bin", "plugin-"+key+".reattach.json")
	}
	return ""
}

// devRepoRoot is the workspace root in a dev/lab run. WICK_DEV_REPO_ROOT is set
// by the wicklab launch config; falls back to the process cwd.
func devRepoRoot() string {
	if r := strings.TrimSpace(os.Getenv("WICK_DEV_REPO_ROOT")); r != "" {
		return r
	}
	wd, _ := os.Getwd()
	return wd
}

// ensureSlotLocked makes room for one new subprocess when at capacity. It
// evicts the least-recently-used idle subprocess, or waits (bounded by
// queueTimeout) when all are busy. Caller holds m.mu. maxProcs <= 0 = unlimited.
func (m *Manager) ensureSlotLocked() error {
	if m.maxProcs <= 0 {
		return nil
	}
	qt := m.queueTimeout
	if qt <= 0 {
		qt = defaultQueueTimeout
	}
	deadline := time.Now().Add(qt)
	for len(m.entries) >= m.maxProcs {
		if m.evictIdleLocked() {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("connector pool full (%d/%d in use), timed out after %s", len(m.entries), m.maxProcs, qt)
		}
		timer := time.AfterFunc(remaining, func() {
			m.mu.Lock()
			m.cond.Broadcast()
			m.mu.Unlock()
		})
		m.cond.Wait()
		timer.Stop()
	}
	return nil
}

// evictIdleLocked kills the LRU idle (inflight==0) subprocess. Caller holds m.mu.
func (m *Manager) evictIdleLocked() bool {
	var lruKey string
	var lruUsed time.Time
	found := false
	for k, e := range m.entries {
		if e.inflight > 0 || m.warm[k] {
			continue
		}
		if !found || e.lastUsed.Before(lruUsed) {
			lruKey, lruUsed, found = k, e.lastUsed, true
		}
	}
	if !found {
		return false
	}
	m.killFn(lruKey)
	delete(m.entries, lruKey)
	return true
}

// leaseLocked builds a Lease for e, whose inflight the caller already
// incremented. Caller holds m.mu.
func (m *Manager) leaseLocked(e *entry) *Lease {
	return &Lease{
		Conn: e.conn,
		release: func() {
			m.mu.Lock()
			if e.inflight > 0 {
				e.inflight--
			}
			e.lastUsed = m.now()
			if m.cond != nil {
				m.cond.Broadcast()
			}
			m.mu.Unlock()
		},
	}
}
