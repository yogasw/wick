package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ApprovalManager owns the per-session approval state on the daemon
// side. Three concerns:
//
//  1. Lifecycle of one Listener per session (Start/StopSession).
//  2. In-memory "approve this session" set, hot path for the gate
//     binary's short-circuit AND for /approve POST decisions arriving
//     while a pending request is open.
//  3. Persistent "always allow" set, written into spec.json so the
//     gate binary can short-circuit without ever dialing the socket.
//
// Concurrency: the manager mutex guards all maps; per-Listener
// concurrency is delegated to Listener itself.
type ApprovalManager struct {
	timeout    func() time.Duration         // injected so tests can shrink
	socketDir  func(sessionID string) string // <session-dir>/gate
	specPath   func(sessionID string) string // <session-dir>/gate/spec.json
	onRequest  func(sessionID string, r ApprovalRequest)
	onResolved func(sessionID, requestID string, decision string)

	mu              sync.Mutex
	listeners       map[string]*Listener        // sessionID → listener
	sessionApproved map[string]map[string]bool  // sessionID → matchKey → true
}

// ApprovalManagerOptions wires the manager to its environment. All
// callbacks are optional; nil = no broadcast.
type ApprovalManagerOptions struct {
	// Timeout overrides DefaultApprovalTimeout for new listeners.
	// Zero = use the default.
	Timeout time.Duration
	// SocketDir computes the per-session socket directory. Required.
	// Output is typically `<layout.SessionDir(id)>/gate`.
	SocketDir func(sessionID string) string
	// SpecPath returns the absolute path to the spec.json the factory
	// wrote for this session. Required for "always allow" persistence.
	// Output is typically `<layout.SessionDir(id)>/gate/spec.json`.
	SpecPath func(sessionID string) string
	// OnRequest fires when the gate binary connects with a new request.
	// Daemon broadcasts this as SSE `approval_request`.
	OnRequest func(sessionID string, r ApprovalRequest)
	// OnResolved fires once a decision is delivered (by any path —
	// UI click, timeout, listener close). Daemon broadcasts this as
	// SSE `approval_resolved` so all open tabs dismiss the modal.
	OnResolved func(sessionID, requestID string, decision string)
}

// NewApprovalManager constructs the manager but starts no listeners.
// Call StartSession to bind a socket per session as it activates.
func NewApprovalManager(opt ApprovalManagerOptions) (*ApprovalManager, error) {
	if opt.SocketDir == nil {
		return nil, fmt.Errorf("ApprovalManager: SocketDir required")
	}
	if opt.SpecPath == nil {
		return nil, fmt.Errorf("ApprovalManager: SpecPath required")
	}
	timeout := opt.Timeout
	return &ApprovalManager{
		timeout:         func() time.Duration { return timeout },
		socketDir:       opt.SocketDir,
		specPath:        opt.SpecPath,
		onRequest:       opt.OnRequest,
		onResolved:      opt.OnResolved,
		listeners:       make(map[string]*Listener),
		sessionApproved: make(map[string]map[string]bool),
	}, nil
}

// SocketPathFor returns the socket path the factory should write
// into spec.SocketPath for sessionID. Centralized so the layout
// stays in one place.
func (m *ApprovalManager) SocketPathFor(sessionID string) string {
	return filepath.Join(m.socketDir(sessionID), "gate.sock")
}

// StartSession binds a Listener for sessionID. Idempotent: a second
// call is a no-op (returns the existing listener's socket path).
func (m *ApprovalManager) StartSession(sessionID string) (string, error) {
	m.mu.Lock()
	if l, ok := m.listeners[sessionID]; ok {
		m.mu.Unlock()
		return l.SocketPath(), nil
	}
	m.mu.Unlock()

	socketPath := m.SocketPathFor(sessionID)
	l, err := NewListener(ListenerOptions{
		SocketPath: socketPath,
		Timeout:    m.timeout(),
		OnRequest: func(r ApprovalRequest) {
			// session-level auto-approve: if the user already clicked
			// "Allow this session" for this matchKey, resolve
			// immediately without ever broadcasting to the UI.
			if m.IsSessionApproved(sessionID, r.MatchKey) {
				m.resolve(sessionID, r.ID, DecisionApproveSession, "session auto-approved")
				return
			}
			if m.onRequest != nil {
				m.onRequest(sessionID, r)
			}
		},
	})
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.listeners[sessionID] = l
	m.mu.Unlock()
	return socketPath, nil
}

// StopSession closes the listener for sessionID and frees its
// in-memory approve-session set. Persistent always-allow entries in
// spec.json are untouched.
func (m *ApprovalManager) StopSession(sessionID string) {
	m.mu.Lock()
	l := m.listeners[sessionID]
	delete(m.listeners, sessionID)
	delete(m.sessionApproved, sessionID)
	m.mu.Unlock()
	if l != nil {
		_ = l.Close()
	}
}

// Stop closes every listener. Used at daemon shutdown.
func (m *ApprovalManager) Stop() {
	m.mu.Lock()
	ls := make([]*Listener, 0, len(m.listeners))
	for _, l := range m.listeners {
		ls = append(ls, l)
	}
	m.listeners = nil
	m.sessionApproved = nil
	m.mu.Unlock()
	for _, l := range ls {
		_ = l.Close()
	}
}

// Resolve delivers a UI decision into the matching pending request.
// Returns false if the request id no longer exists (already timed
// out or resolved). Side effects:
//
//   - approve_session: records matchKey in the in-memory set so
//     later requests for the same command auto-resolve.
//   - approve_always: records matchKey in the in-memory set AND
//     rewrites spec.json with the updated AutoApproved list, so the
//     next spawn's gate binary skips the round-trip entirely.
func (m *ApprovalManager) Resolve(sessionID, requestID, decision, reason, matchKey string) (bool, error) {
	switch decision {
	case DecisionApproveSession:
		m.markSessionApproved(sessionID, matchKey)
	case DecisionApproveAlways:
		m.markSessionApproved(sessionID, matchKey)
		if err := m.appendAlwaysAllow(sessionID, matchKey); err != nil {
			return false, fmt.Errorf("persist always-allow: %w", err)
		}
	}
	ok := m.resolve(sessionID, requestID, decision, reason)
	return ok, nil
}

// resolve is the unsafe inner: deliver to the listener channel +
// fire OnResolved. Caller is responsible for any state-mutation that
// should happen before the gate binary unblocks.
func (m *ApprovalManager) resolve(sessionID, requestID, decision, reason string) bool {
	m.mu.Lock()
	l := m.listeners[sessionID]
	m.mu.Unlock()
	if l == nil {
		return false
	}
	ok := l.Resolve(requestID, decision, reason)
	if ok && m.onResolved != nil {
		m.onResolved(sessionID, requestID, decision)
	}
	return ok
}

// IsSessionApproved reports whether the user clicked "Allow this
// session" for matchKey in sessionID's current pool lifetime.
func (m *ApprovalManager) IsSessionApproved(sessionID, matchKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.sessionApproved[sessionID]
	return set != nil && set[matchKey]
}

// SessionApprovedKeys returns the in-memory approve-session list
// for one session. Used by the UI to render the "Approved commands"
// panel.
func (m *ApprovalManager) SessionApprovedKeys(sessionID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.sessionApproved[sessionID]
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PendingFor returns the listener's snapshot of in-flight requests
// for sessionID, or nil if no listener is bound. Used by the UI for
// reconnection rehydrate.
func (m *ApprovalManager) PendingFor(sessionID string) []ApprovalRequest {
	m.mu.Lock()
	l := m.listeners[sessionID]
	m.mu.Unlock()
	if l == nil {
		return nil
	}
	return l.PendingSnapshot()
}

// AutoApprovedFor returns the persistent always-allow list for
// sessionID by reading spec.json. Used by pool.GateConfig.AutoApprovedFor
// at Build time so the gate binary's spec gets pre-populated.
func (m *ApprovalManager) AutoApprovedFor(sessionID string) []string {
	spec, err := m.readSpec(sessionID)
	if err != nil {
		return nil
	}
	return append([]string(nil), spec.AutoApproved...)
}

// RevokeAlways removes matchKey from spec.json's AutoApproved list +
// from the in-memory session set. The next dispatched spec (next
// spawn) will reflect the change; a session already running keeps
// the old list until it respawns — that's intentional, the user
// already accepted those when they clicked "Always" earlier.
func (m *ApprovalManager) RevokeAlways(sessionID, matchKey string) error {
	spec, err := m.readSpec(sessionID)
	if err != nil {
		return err
	}
	out := spec.AutoApproved[:0]
	for _, k := range spec.AutoApproved {
		if k != matchKey {
			out = append(out, k)
		}
	}
	spec.AutoApproved = out
	if err := m.writeSpec(sessionID, spec); err != nil {
		return err
	}
	m.mu.Lock()
	if set := m.sessionApproved[sessionID]; set != nil {
		delete(set, matchKey)
	}
	m.mu.Unlock()
	return nil
}

// RevokeSession removes matchKey from the in-memory approve-session
// set only — the always-allow list (if any) is untouched.
func (m *ApprovalManager) RevokeSession(sessionID, matchKey string) {
	m.mu.Lock()
	if set := m.sessionApproved[sessionID]; set != nil {
		delete(set, matchKey)
	}
	m.mu.Unlock()
}

func (m *ApprovalManager) markSessionApproved(sessionID, matchKey string) {
	if matchKey == "" {
		return
	}
	m.mu.Lock()
	set, ok := m.sessionApproved[sessionID]
	if !ok {
		set = make(map[string]bool)
		m.sessionApproved[sessionID] = set
	}
	set[matchKey] = true
	m.mu.Unlock()
}

func (m *ApprovalManager) appendAlwaysAllow(sessionID, matchKey string) error {
	if matchKey == "" {
		return nil
	}
	spec, err := m.readSpec(sessionID)
	if err != nil {
		return err
	}
	for _, k := range spec.AutoApproved {
		if k == matchKey {
			return nil // already there
		}
	}
	spec.AutoApproved = append(spec.AutoApproved, matchKey)
	return m.writeSpec(sessionID, spec)
}

func (m *ApprovalManager) readSpec(sessionID string) (Spec, error) {
	path := m.specPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read spec %s: %w", path, err)
	}
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("parse spec %s: %w", path, err)
	}
	return s, nil
}

func (m *ApprovalManager) writeSpec(sessionID string, s Spec) error {
	path := m.specPath(sessionID)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write spec %s: %w", path, err)
	}
	return nil
}
