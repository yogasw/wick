package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ApprovalRequest is what the gate binary sends over the unix
// socket when it needs an interactive decision. Daemon decodes one
// per connection, then blocks until a UI POST arrives or the
// timeout fires.
type ApprovalRequest struct {
	ID        string `json:"id"`         // UUID minted by the gate binary
	SessionID string `json:"session_id"` // also encoded in spec, but echo for clarity
	AgentName string `json:"agent_name"`
	Tool      string `json:"tool"`     // "Bash", "Edit", ...
	Cmd       string `json:"cmd"`      // raw command string
	WorkDir   string `json:"work_dir"` // cwd at exec time
	MatchKey  string `json:"match_key"`
	Timestamp int64  `json:"ts"` // unix ms
}

// ApprovalResponse is the daemon's reply. The gate binary maps
// Decision to an exit code: any "approve_*" → 0, "block" → 2.
type ApprovalResponse struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // "approve_once" | "approve_session" | "approve_always" | "block"
	Reason   string `json:"reason,omitempty"`
}

// Decision values. Kept as string consts so JSON wire format stays
// stable across daemon + binary builds; renames are loud.
const (
	DecisionApproveOnce    = "approve_once"
	DecisionApproveSession = "approve_session"
	DecisionApproveAlways  = "approve_always"
	DecisionBlock          = "block"
)

// IsApprove reports whether a decision string means "let it run".
// Anything else is treated as block by the binary.
func IsApprove(d string) bool {
	switch d {
	case DecisionApproveOnce, DecisionApproveSession, DecisionApproveAlways:
		return true
	}
	return false
}

// pendingApproval is one in-flight request waiting for a UI decision.
type pendingApproval struct {
	req ApprovalRequest
	ch  chan ApprovalResponse // buffered cap 1
}

// Listener owns a Unix domain socket per session. Connections from
// the gate binary land here, get registered as pending, and resolve
// when the UI calls Resolve(id, decision) — or when the timeout
// fires.
//
// One Listener per session. Sessions whose gate disabled (no socket)
// just don't have a Listener at all.
type Listener struct {
	socketPath string
	timeout    time.Duration
	onRequest  func(ApprovalRequest) // fired when a request lands; daemon broadcasts SSE here

	ln net.Listener

	mu      sync.Mutex
	pending map[string]*pendingApproval

	stopOnce sync.Once
	stopped  chan struct{}
}

// ListenerOptions configures NewListener. Timeout default = 25s
// (hook timeout on claude is 30s, leaving headroom for the gate
// binary to exit cleanly with the daemon's reply).
type ListenerOptions struct {
	SocketPath string
	Timeout    time.Duration
	OnRequest  func(ApprovalRequest) // called once per incoming request
}

// DefaultApprovalTimeout matches the doc commitment in
// command-gate-architecture.md §5.3. 25s < hook timeout 30s so the
// gate binary always exits cleanly before claude times out.
const DefaultApprovalTimeout = 25 * time.Second

// NewListener binds the unix socket at opt.SocketPath and starts the
// accept loop in a goroutine. Caller must Close() to clean up.
//
// The socket file is recreated each call: stale leftovers from a
// crashed previous run are removed, then permissions locked to 0600
// so only the owner uid can connect.
func NewListener(opt ListenerOptions) (*Listener, error) {
	if opt.SocketPath == "" {
		return nil, errors.New("gate.NewListener: SocketPath required")
	}
	if err := os.MkdirAll(filepath.Dir(opt.SocketPath), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir socket parent: %w", err)
	}
	// Stale socket from a previous run — remove. ENOENT is fine.
	if err := os.Remove(opt.SocketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", opt.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %q: %w", opt.SocketPath, err)
	}
	// chmod after Listen so the bind happens with the umask but the
	// final perms are exact 0600. On Windows this is a no-op for
	// unix sockets; the security comes from the parent dir 0700.
	_ = os.Chmod(opt.SocketPath, 0o600)

	if opt.Timeout <= 0 {
		opt.Timeout = DefaultApprovalTimeout
	}

	l := &Listener{
		socketPath: opt.SocketPath,
		timeout:    opt.Timeout,
		onRequest:  opt.OnRequest,
		ln:         ln,
		pending:    make(map[string]*pendingApproval),
		stopped:    make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

// Close stops the accept loop, fails any pending requests with
// "block (listener closed)", and removes the socket file.
func (l *Listener) Close() error {
	l.stopOnce.Do(func() {
		close(l.stopped)
		_ = l.ln.Close()

		l.mu.Lock()
		for id, p := range l.pending {
			select {
			case p.ch <- ApprovalResponse{
				ID:       id,
				Decision: DecisionBlock,
				Reason:   "listener closed",
			}:
			default:
			}
		}
		l.pending = nil
		l.mu.Unlock()

		_ = os.Remove(l.socketPath)
	})
	return nil
}

// SocketPath returns the bound socket path.
func (l *Listener) SocketPath() string { return l.socketPath }

// Resolve delivers a decision to the goroutine handling the matching
// pending request. Safe to call from any goroutine. Returns false if
// the id is unknown (timed out, already resolved, or never seen).
func (l *Listener) Resolve(id string, decision string, reason string) bool {
	l.mu.Lock()
	p, ok := l.pending[id]
	if ok {
		delete(l.pending, id)
	}
	l.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case p.ch <- ApprovalResponse{ID: id, Decision: decision, Reason: reason}:
		return true
	default:
		// Connection goroutine already gave up (timeout). Drop.
		return false
	}
}

// PendingSnapshot returns a copy of currently-pending requests.
// Useful for the UI's "approval queue" view + reconnection rehydrate.
func (l *Listener) PendingSnapshot() []ApprovalRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ApprovalRequest, 0, len(l.pending))
	for _, p := range l.pending {
		out = append(out, p.req)
	}
	return out
}

func (l *Listener) acceptLoop() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.stopped:
				return
			default:
			}
			// Accept errors on a still-running listener are
			// transient (interrupted, momentary fd exhaustion).
			// Sleep briefly + retry; fail-fast if perma-broken.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go l.handleConn(conn)
	}
}

func (l *Listener) handleConn(conn net.Conn) {
	defer conn.Close()

	// Read deadline = timeout + small buffer so a stuck client
	// doesn't pin a goroutine forever. The decision wait below
	// uses its own timer.
	_ = conn.SetReadDeadline(time.Now().Add(l.timeout + 5*time.Second))

	var req ApprovalRequest
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		// Bad request — log line not worth the noise; just close.
		return
	}
	if req.ID == "" {
		return
	}

	ch := make(chan ApprovalResponse, 1)
	l.mu.Lock()
	l.pending[req.ID] = &pendingApproval{req: req, ch: ch}
	l.mu.Unlock()

	if l.onRequest != nil {
		// Best-effort fan-out to the daemon's broadcaster. Run in a
		// goroutine so a slow handler can't stall the conn timer.
		go l.onRequest(req)
	}

	timer := time.NewTimer(l.timeout)
	defer timer.Stop()

	var resp ApprovalResponse
	select {
	case resp = <-ch:
		// Resolved by UI.
	case <-timer.C:
		// Timeout — pull from pending so a late Resolve doesn't try
		// to deliver to a dead channel.
		l.mu.Lock()
		delete(l.pending, req.ID)
		l.mu.Unlock()
		resp = ApprovalResponse{
			ID:       req.ID,
			Decision: DecisionBlock,
			Reason:   "timeout",
		}
	case <-l.stopped:
		resp = ApprovalResponse{
			ID:       req.ID,
			Decision: DecisionBlock,
			Reason:   "listener closed",
		}
	}

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = json.NewEncoder(conn).Encode(resp)
}
