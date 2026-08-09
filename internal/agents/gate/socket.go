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
	Timestamp int64  `json:"ts"`              // unix ms
	Probe     bool   `json:"probe,omitempty"` // doctor health-check — server auto-replies immediately
	// ExpiresInSec is set by the daemon when it broadcasts the request
	// to browsers, and only when a fixed deadline applies. Zero means
	// the prompt waits for as long as somebody is watching, so the UI
	// must not run a countdown — it has no end to count towards.
	// Never sent by the gate binary; purely daemon → UI.
	ExpiresInSec int `json:"expires_in_sec,omitempty"`
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
	DecisionApproveAll     = "approve_all" // approve every future command in this session
	DecisionApproveAlways  = "approve_always"
	DecisionBlock          = "block"
	// DecisionGuide refuses this one command but lets the agent keep
	// working: the user's reason is handed to the model as feedback so
	// it can take a different route. DecisionBlock ends the turn.
	DecisionGuide = "guide"
)

// IsBlock reports whether a decision means "don't run it". Both refusal
// decisions answer yes; they differ only in whether the agent's turn
// survives, which is the gate binary's concern, not the caller's.
func IsBlock(d string) bool {
	return d == DecisionBlock || d == DecisionGuide
}

// HaltsTurn reports whether a decision should stop the agent entirely
// rather than just refuse the call.
func HaltsTurn(d string) bool { return d == DecisionBlock }

// IsApprove reports whether a decision string means "let it run".
// Anything else is treated as block by the binary.
func IsApprove(d string) bool {
	switch d {
	case DecisionApproveOnce, DecisionApproveSession, DecisionApproveAll, DecisionApproveAlways:
		return true
	}
	return false
}

// pendingApproval is one in-flight request waiting for a UI decision.
type pendingApproval struct {
	req ApprovalRequest
	ch  chan ApprovalResponse // buffered cap 1
}

// WaitPolicy decides how long an approval prompt stays open. Two
// shapes, mirroring the two Permission Gate config knobs:
//
//   - TimeoutEnabled: fixed deadline. Timeout elapses → block.
//   - !TimeoutEnabled: the prompt lives as long as a browser tab is
//     watching the session. HasViewer going false starts a grace
//     window; only when that expires with still no viewer does the
//     request block. Without a HasViewer func this degenerates to the
//     fixed deadline, so a caller that can't observe viewers (tests,
//     the doctor probe) keeps the old behaviour.
//
// Resolved per request, so flipping the config mid-session applies to
// the next prompt without a daemon restart.
type WaitPolicy struct {
	TimeoutEnabled bool
	Timeout        time.Duration
	// HasViewer reports whether a browser is currently subscribed to
	// the SSE stream of the session this request belongs to. Nil =
	// unknown; treated as "no viewer tracking available" and the fixed
	// deadline is used instead. The policy is built per request, so the
	// session is already bound inside this closure.
	HasViewer func() bool
	// Grace is how long a request survives after the last viewer
	// disappears. Sized above the SSE keepalive interval so a reload
	// or a brief reconnect doesn't read as "nobody is home".
	Grace time.Duration
}

// DefaultViewerGrace is the fallback for WaitPolicy.Grace. The SSE
// handlers keep alive every 15s, which is how fast a dropped tab is
// noticed at worst — so anything at or below that would block prompts
// during an ordinary page reload.
const DefaultViewerGrace = 20 * time.Second

// viewerPollInterval is how often the wait loop re-checks viewer
// presence. Cheap (a mutex + map lookup), so keep it well under Grace:
// the grace window can only be measured in multiples of this, and it
// also bounds how long a resolved-elsewhere request lingers.
const viewerPollInterval = 500 * time.Millisecond

// timeout returns the fixed deadline this policy should use, falling
// back to DefaultApprovalTimeout when unset.
func (p WaitPolicy) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultApprovalTimeout
}

// grace returns the viewer-loss grace window, falling back to
// DefaultViewerGrace when unset.
func (p WaitPolicy) grace() time.Duration {
	if p.Grace > 0 {
		return p.Grace
	}
	return DefaultViewerGrace
}

// waitsForViewer reports whether this policy should track browser
// presence rather than run a fixed deadline.
func (p WaitPolicy) waitsForViewer() bool {
	return !p.TimeoutEnabled && p.HasViewer != nil
}

// awaitDecision blocks until a decision arrives on ch, the policy
// gives up, or stopped closes. It never returns a zero response: every
// exit path produces a decision the caller can hand straight back to
// the gate binary.
//
// onGiveUp runs before returning a block so the caller can evict the
// request from its pending set — a late Resolve must not try to
// deliver into a channel nobody reads any more.
func (p WaitPolicy) awaitDecision(
	id string,
	ch <-chan ApprovalResponse,
	stopped <-chan struct{},
	ctxDone <-chan struct{},
	onGiveUp func(),
) ApprovalResponse {
	giveUp := func(reason string) ApprovalResponse {
		if onGiveUp != nil {
			onGiveUp()
		}
		return ApprovalResponse{ID: id, Decision: DecisionBlock, Reason: reason}
	}

	if !p.waitsForViewer() {
		timer := time.NewTimer(p.timeout())
		defer timer.Stop()
		select {
		case resp := <-ch:
			return resp
		case <-timer.C:
			return giveUp("timeout")
		case <-stopped:
			return giveUp("listener closed")
		case <-ctxDone:
			return giveUp("cancelled")
		}
	}

	// Viewer-tracking mode. lastSeen is the most recent moment a tab
	// was observed; it starts now so a request that arrives during a
	// reload gets the full grace window before being judged.
	ticker := time.NewTicker(viewerPollInterval)
	defer ticker.Stop()
	grace := p.grace()
	lastSeen := time.Now()

	for {
		select {
		case resp := <-ch:
			return resp
		case <-stopped:
			return giveUp("listener closed")
		case <-ctxDone:
			return giveUp("cancelled")
		case <-ticker.C:
			if p.HasViewer() {
				lastSeen = time.Now()
				continue
			}
			if time.Since(lastSeen) >= grace {
				return giveUp("no viewer")
			}
		}
	}
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
	// policy is resolved per request so a config change (deadline on/off,
	// new timeout) applies to the next prompt without rebinding the socket.
	policy    func(ApprovalRequest) WaitPolicy
	onRequest func(ApprovalRequest) // fired when a request lands; daemon broadcasts SSE here
	// onExpired fires when a request is decided by the wait loop rather
	// than by a user — timeout, no viewer, listener shutdown. Without
	// it the browser never learns the prompt died and keeps showing a
	// modal whose buttons can only ever return "no longer pending".
	onExpired func(req ApprovalRequest, decision, reason string)

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
	// Policy, when set, is consulted once per request and overrides
	// Timeout entirely — that's how the "wait while a tab is open"
	// mode reaches the wait loop. It receives the request because
	// viewer lookup needs the WICK session id, which only the caller
	// can derive (r.SessionID is claude's own id; the wick session is
	// resolved from r.WorkDir). Nil = fixed deadline from Timeout.
	Policy func(ApprovalRequest) WaitPolicy
	// OnExpired is called when the wait loop decides on its own — no
	// user ever clicked. Daemon broadcasts this as approval_resolved
	// so open modals close instead of lingering as dead UI.
	OnExpired func(req ApprovalRequest, decision, reason string)
}

// DefaultApprovalTimeout is the deadline used when the Permission
// Gate's approval timeout is switched on without an explicit value.
// It also remains the fallback for callers that supply no policy at
// all (the doctor probe, tests).
const DefaultApprovalTimeout = 25 * time.Second

// requestDecodeTimeout bounds how long a connected client may take to
// write its ApprovalRequest. Separate from the decision wait so the
// wait-for-viewer mode can block indefinitely without also letting a
// silent connection hold a goroutine open forever.
const requestDecodeTimeout = 10 * time.Second

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
	policy := opt.Policy
	if policy == nil {
		fixed := WaitPolicy{TimeoutEnabled: true, Timeout: opt.Timeout}
		policy = func(ApprovalRequest) WaitPolicy { return fixed }
	}

	l := &Listener{
		socketPath: opt.SocketPath,
		policy:     policy,
		onRequest:  opt.OnRequest,
		onExpired:  opt.OnExpired,
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

// LookupPending returns the ApprovalRequest for id without removing it.
// Used by the approval handler to retrieve the Cmd before resolving.
func (l *Listener) LookupPending(id string) (ApprovalRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.pending[id]
	if !ok {
		return ApprovalRequest{}, false
	}
	return p.req, true
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

	// Read deadline covers only the request decode, not the decision
	// wait — that has its own policy below. Kept short and fixed so a
	// client that connects and never writes can't pin a goroutine, even
	// in the wait-for-viewer mode where the decision itself is unbounded.
	_ = conn.SetReadDeadline(time.Now().Add(requestDecodeTimeout))

	var req ApprovalRequest
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		// Bad request — log line not worth the noise; just close.
		return
	}
	if req.ID == "" {
		return
	}

	// Probe request from `wick doctor` — reply immediately without
	// queuing into pending or firing onRequest.
	if req.Probe {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = json.NewEncoder(conn).Encode(ApprovalResponse{
			ID:       req.ID,
			Decision: DecisionApproveOnce,
			Reason:   "probe",
		})
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

	// Giving up must evict the request from pending, so a late Resolve
	// reports "no longer pending" instead of writing into a channel
	// this goroutine has stopped reading.
	expired := false
	resp := l.policy(req).awaitDecision(req.ID, ch, l.stopped, nil, func() {
		expired = true
		l.mu.Lock()
		delete(l.pending, req.ID)
		l.mu.Unlock()
	})
	// Tell the daemon so it can close any modal still showing this
	// request. Only for wait-loop decisions: a user-driven Resolve
	// already broadcasts from the manager.
	if expired && l.onExpired != nil {
		l.onExpired(req, resp.Decision, resp.Reason)
	}

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = json.NewEncoder(conn).Encode(resp)
}
