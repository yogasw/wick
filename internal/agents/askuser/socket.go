package askuser

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

// Asker is the transport-agnostic ask contract. The in-process
// Manager implements it for the HTTP server; SocketAsker implements
// it for sibling processes (stdio MCP) by dialing the server's unix
// socket — same pattern as the gate binary dialing gate.sock, so an
// ask works from any local process without HTTP auth.
type Asker interface {
	Ask(q Question, done <-chan struct{}) (Answer, error)
}

// SocketPath returns the unix socket the server's ask listener binds
// and sibling processes dial. Lives next to agentctl.sock / the gate
// dir; security comes from the 0700 parent dir, like gate.sock.
//
// Layout: ~/.<app>/agents/askuser.sock
func SocketPath(appName string) string {
	name := appName
	if name == "" {
		name = "wick"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "."+name, "agents", "askuser.sock")
}

// socketRequest is the wire envelope: the Question plus its timeout
// (Question.Timeout is json:"-" so it needs an explicit field).
type socketRequest struct {
	Question
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// socketResponse mirrors Answer plus an error string so the dialer
// can distinguish "user declined" from "ask failed".
type socketResponse struct {
	Value  string            `json:"value,omitempty"`
	Text   string            `json:"text,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// connGrace is added on top of the ask timeout for socket deadlines
// so the transport never wins the race against the ask timer.
const connGrace = 10 * time.Second

// SocketServer accepts ask requests from sibling processes and
// resolves them through the wrapped Manager (pending map + SSE +
// web-UI answer — identical path to an in-process ask).
type SocketServer struct {
	mgr  *Manager
	ln   net.Listener
	path string

	stopOnce sync.Once
	stopped  chan struct{}
}

// ServeSocket binds path and serves asks against mgr until Close.
// Stale socket files from a crashed previous run are removed first,
// mirroring gate.NewListener.
func ServeSocket(path string, mgr *Manager) (*SocketServer, error) {
	if path == "" {
		return nil, errors.New("askuser.ServeSocket: path required")
	}
	if mgr == nil {
		return nil, errors.New("askuser.ServeSocket: manager required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir socket parent: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen unix %q: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)

	s := &SocketServer{mgr: mgr, ln: ln, path: path, stopped: make(chan struct{})}
	go s.acceptLoop()
	return s, nil
}

// Close stops the accept loop and removes the socket file. In-flight
// asks resolve through their own conn-scoped done channels.
func (s *SocketServer) Close() error {
	s.stopOnce.Do(func() {
		close(s.stopped)
		_ = s.ln.Close()
		_ = os.Remove(s.path)
	})
	return nil
}

// SocketPath returns the bound socket path.
func (s *SocketServer) SocketPath() string { return s.path }

func (s *SocketServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stopped:
				return
			default:
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *SocketServer) handleConn(conn net.Conn) {
	defer conn.Close()

	var req socketRequest
	_ = conn.SetReadDeadline(time.Now().Add(connGrace))
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	q := req.Question
	if req.TimeoutMS > 0 {
		q.Timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	timeout := q.Timeout
	if timeout <= 0 {
		timeout = s.mgr.defaultTimeout
	}

	// Cancel the pending ask if the dialer goes away: a read on a
	// one-request connection only returns when the peer closes (EOF)
	// or the deadline fires. The deadline doubles as a hard ceiling
	// so a stuck peer can't pin this goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = conn.SetReadDeadline(time.Now().Add(timeout + connGrace))
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
	}()

	ans, err := s.mgr.Ask(q, done)
	resp := socketResponse{Value: ans.Value, Text: ans.Text, Values: ans.Values}
	if err != nil {
		resp.Error = err.Error()
	}
	_ = conn.SetWriteDeadline(time.Now().Add(connGrace))
	_ = json.NewEncoder(conn).Encode(resp)
}

// SocketAsker dials the server's askuser socket. Implements Asker so
// the MCP handlers can use it interchangeably with the in-process
// Manager. Construction is cheap and dial happens per Ask — the
// server may start or restart at any time relative to this process.
type SocketAsker struct {
	Path string
}

// Ask sends one Question over the socket and blocks until the answer
// comes back, the server-side timeout fires, or done closes. A dial
// failure means no wick server (or one too old to serve asks) is
// running on this machine.
func (a *SocketAsker) Ask(q Question, done <-chan struct{}) (Answer, error) {
	if a == nil || a.Path == "" {
		return Answer{}, errors.New("askuser: socket path not configured")
	}
	timeout := q.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conn, err := net.DialTimeout("unix", a.Path, 5*time.Second)
	if err != nil {
		return Answer{}, fmt.Errorf("askuser: wick agents UI not reachable (is the wick server running?): %w", err)
	}
	defer conn.Close()

	// done closed (MCP client gave up) → close the conn; the server
	// side sees EOF and cancels its pending ask.
	finished := make(chan struct{})
	defer close(finished)
	if done != nil {
		go func() {
			select {
			case <-done:
				_ = conn.Close()
			case <-finished:
			}
		}()
	}

	_ = conn.SetWriteDeadline(time.Now().Add(connGrace))
	if err := json.NewEncoder(conn).Encode(socketRequest{Question: q, TimeoutMS: timeout.Milliseconds()}); err != nil {
		return Answer{}, fmt.Errorf("askuser: send over socket: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout + connGrace))
	var resp socketResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if done != nil {
			select {
			case <-done:
				return Answer{}, errors.New("askuser: cancelled by caller")
			default:
			}
		}
		return Answer{}, fmt.Errorf("askuser: read over socket: %w", err)
	}
	if resp.Error != "" {
		return Answer{}, errors.New(resp.Error)
	}
	return Answer{Value: resp.Value, Text: resp.Text, Values: resp.Values}, nil
}
