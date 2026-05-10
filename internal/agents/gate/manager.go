package gate

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ApprovalManager owns the daemon-side approval state. Three concerns:
//
//  1. Lifecycle of one shared Listener (Start/Stop) — single socket
//     at SharedSocketPath(appName).
//  2. In-memory "approve this session" set, hot path for /approve
//     POST decisions arriving while a pending request is open.
//  3. Persistent "always allow" set, written into the shared
//     spec.json so the gate binary can short-circuit without ever
//     dialing the socket.
//
// Pre-Stage 9 the manager owned one Listener per session; Stage 9
// folded that into a single shared listener with cwd-based session
// routing supplied by the caller (RouteByCWD callback).
//
// Concurrency: the manager mutex guards all maps; Listener handles
// its own internal concurrency.
type ApprovalManager struct {
	appName    string
	timeout    func() time.Duration
	routeByCWD func(cwd string) (sessionID string, ok bool)
	onRequest  func(sessionID string, r ApprovalRequest)
	onResolved func(sessionID, requestID, decision string)

	mu                  sync.Mutex
	listener            *Listener
	sessionApproved     map[string]map[string]bool // sessionID → matchKey → true
	sessionAllApproved  map[string]bool            // sessionID → approve every command
}

// ApprovalManagerOptions wires the manager to its environment.
type ApprovalManagerOptions struct {
	// AppName drives SharedSocketPath / SharedSpecPath. Required.
	AppName string
	// Timeout overrides DefaultApprovalTimeout. Zero = default.
	Timeout time.Duration
	// RouteByCWD maps a hook payload's cwd to the wick sessionID
	// that owns that workspace. Required — without it the manager
	// can't tag inbound requests with a session for SSE broadcast.
	// Daemon implementation typically scans active session metadata
	// for a matching workspace prefix.
	RouteByCWD func(cwd string) (sessionID string, ok bool)
	// OnRequest fires when the gate binary connects with a new
	// request, AFTER cwd→session routing succeeds. Daemon
	// broadcasts as SSE `approval_request`.
	OnRequest func(sessionID string, r ApprovalRequest)
	// OnResolved fires once a decision is delivered. Daemon
	// broadcasts as SSE `approval_resolved`.
	OnResolved func(sessionID, requestID, decision string)
}

// NewApprovalManager constructs the manager but starts no listener.
// Call Start to bind the shared socket.
func NewApprovalManager(opt ApprovalManagerOptions) (*ApprovalManager, error) {
	if opt.AppName == "" {
		return nil, fmt.Errorf("ApprovalManager: AppName required")
	}
	if opt.RouteByCWD == nil {
		return nil, fmt.Errorf("ApprovalManager: RouteByCWD required")
	}
	timeout := opt.Timeout
	return &ApprovalManager{
		appName:            opt.AppName,
		timeout:            func() time.Duration { return timeout },
		routeByCWD:         opt.RouteByCWD,
		onRequest:          opt.OnRequest,
		onResolved:         opt.OnResolved,
		sessionApproved:    make(map[string]map[string]bool),
		sessionAllApproved: make(map[string]bool),
	}, nil
}

// Start binds the shared listener at SharedSocketPath(appName).
// Idempotent: a second call is a no-op.
func (m *ApprovalManager) Start() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener != nil {
		return m.listener.SocketPath(), nil
	}
	socketPath := SharedSocketPath(m.appName)
	l, err := NewListener(ListenerOptions{
		SocketPath: socketPath,
		Timeout:    m.timeout(),
		OnRequest:  m.handleRequest,
	})
	if err != nil {
		return "", err
	}
	m.listener = l
	return socketPath, nil
}

// Stop closes the shared listener. Used at daemon shutdown.
func (m *ApprovalManager) Stop() {
	m.mu.Lock()
	l := m.listener
	m.listener = nil
	m.sessionApproved = nil
	m.sessionAllApproved = nil
	m.mu.Unlock()
	if l != nil {
		_ = l.Close()
	}
}

// handleRequest is the per-request entry from the listener. We
// route by cwd to find the wick session, short-circuit on
// session-already-approved, otherwise fan out to the broadcaster.
func (m *ApprovalManager) handleRequest(r ApprovalRequest) {
	sessionID, ok := m.routeByCWD(r.WorkDir)
	if !ok {
		// No session owns this cwd. Fall through to UI broadcast
		// with the empty sessionID; UI can render under "Unrouted"
		// bucket, or admins can revoke via cwd path.
		sessionID = ""
	}
	if sessionID != "" && m.IsSessionAllApproved(sessionID) {
		m.resolve(sessionID, r.ID, DecisionApproveAll, "session all-approved")
		return
	}
	if sessionID != "" && m.IsSessionApproved(sessionID, r.MatchKey) {
		m.resolve(sessionID, r.ID, DecisionApproveSession, "session auto-approved")
		return
	}
	if m.onRequest != nil {
		m.onRequest(sessionID, r)
	}
}

// Resolve delivers a UI decision into the matching pending request.
// Returns false if the request id no longer exists. Side effects:
//
//   - approve_session: records matchKey in the in-memory set so
//     later requests for the same command auto-resolve.
//   - approve_always: records matchKey in the in-memory set AND
//     rewrites the shared spec.json with the updated AutoApproved
//     list so future invocations short-circuit without round-trip.
func (m *ApprovalManager) Resolve(sessionID, requestID, decision, reason, matchKey string) (bool, error) {
	switch decision {
	case DecisionApproveSession:
		m.markSessionApproved(sessionID, matchKey)
	case DecisionApproveAll:
		m.markSessionAllApproved(sessionID)
	case DecisionApproveAlways:
		m.markSessionApproved(sessionID, matchKey)
		if err := m.appendAlwaysAllow(matchKey); err != nil {
			return false, fmt.Errorf("persist always-allow: %w", err)
		}
	}
	ok := m.resolve(sessionID, requestID, decision, reason)
	return ok, nil
}

// resolve is the unsafe inner: deliver to the listener channel +
// fire OnResolved.
func (m *ApprovalManager) resolve(sessionID, requestID, decision, reason string) bool {
	m.mu.Lock()
	l := m.listener
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

// IsSessionAllApproved reports whether the user clicked "Allow All for Session",
// which bypasses per-command hash checks for every future request in the session.
func (m *ApprovalManager) IsSessionAllApproved(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionAllApproved != nil && m.sessionAllApproved[sessionID]
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
// for one session. Used by the UI to render "Approved commands".
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

// PendingFor returns the listener's snapshot of in-flight requests.
// Stage 9 made this global — sessionID is ignored. Kept on the
// signature so the JSON view-model field name on the UI side stays
// readable (`pendingFor(sessionID)` reads better than `pending()`).
func (m *ApprovalManager) PendingFor(_ string) []ApprovalRequest {
	m.mu.Lock()
	l := m.listener
	m.mu.Unlock()
	if l == nil {
		return nil
	}
	return l.PendingSnapshot()
}

// AutoApproved returns the persistent always-allow list from the
// shared spec.json. Used by pool.GateConfig to pre-populate the
// gate's runtime spec view (now read directly from disk by the
// gate binary; this method is retained for compatibility / rendering).
func (m *ApprovalManager) AutoApproved() []string {
	spec, err := LoadSpec(m.appName)
	if err != nil {
		return nil
	}
	return append([]string(nil), spec.AutoApproved...)
}

// RevokeAlways removes matchKey from the shared spec.json AutoApproved
// list. Affects every running gate invocation as soon as the next
// LoadSpec on disk happens (i.e., next hook fire) — gate re-reads
// per call so changes propagate without restart.
func (m *ApprovalManager) RevokeAlways(sessionID, matchKey string) error {
	spec, err := LoadSpec(m.appName)
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
	if err := WriteSharedSpec(m.appName, spec); err != nil {
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

// LookupPending returns the ApprovalRequest for requestID without
// removing it from the pending set. Used by the approval handler to
// retrieve the Cmd before calling Resolve so approve_always can write
// the command back to the persistent allowed_cmds config.
func (m *ApprovalManager) LookupPending(requestID string) (ApprovalRequest, bool) {
	m.mu.Lock()
	l := m.listener
	m.mu.Unlock()
	if l == nil {
		return ApprovalRequest{}, false
	}
	return l.LookupPending(requestID)
}

// SocketPath returns the bound socket path. Empty if Start hasn't
// been called or the listener failed to bind.
func (m *ApprovalManager) SocketPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener == nil {
		return ""
	}
	return m.listener.SocketPath()
}

func (m *ApprovalManager) markSessionAllApproved(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	if m.sessionAllApproved != nil {
		m.sessionAllApproved[sessionID] = true
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

// appendAlwaysAllow appends matchKey to the shared spec.json. Idempotent.
func (m *ApprovalManager) appendAlwaysAllow(matchKey string) error {
	if matchKey == "" {
		return nil
	}
	spec, err := LoadSpec(m.appName)
	if err != nil {
		return err
	}
	for _, k := range spec.AutoApproved {
		if k == matchKey {
			return nil
		}
	}
	spec.AutoApproved = append(spec.AutoApproved, matchKey)
	return WriteSharedSpec(m.appName, spec)
}
