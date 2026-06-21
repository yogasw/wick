package plugin

import (
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
