// Package terminal runs a web terminal (gotty) for one omp/opencode
// instance: an admin picks a command from a fixed allowlist, wick starts
// gotty on 127.0.0.1 with a random port, a random credential and
// --once, and the browser reaches it only through wick's own reverse
// proxy (Session.ServeHTTP) — behind the wick login and the provider
// admin guard. The credential never leaves the server: the proxy adds it
// upstream and blanks it in what the browser receives.
//
// A session ends when the websocket closes, when it idles past
// IdleTimeout, when gotty exits on its own (nobody connected within its
// --timeout), or on wick shutdown. Ending kills gotty and every process
// under it. Every start/end is logged for audit (who, when, instance,
// command) — never the keystrokes.
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	provider "github.com/yogasw/wick/internal/agents/provider"

	"github.com/yogasw/wick/pkg/safeexec"
)

// DefaultIdleTimeout ends a session nobody has typed into or read from.
const DefaultIdleTimeout = 15 * time.Minute

// MaxSessions bounds concurrently open terminals, host-wide.
const MaxSessions = 4

// ErrTooMany is returned when MaxSessions terminals are already open.
var ErrTooMany = errors.New("too many open terminals; close one first")

// WrapFunc wraps a spawn in the agent memory scope (MemGuard), returning
// the argv to exec and a release for after it exits. nil = unwrapped.
type WrapFunc func(bin string, args []string) (string, []string, func())

// StartRequest is one terminal to open.
type StartRequest struct {
	Instance provider.Instance
	Command  Command
	GottyBin string // absolute path of gotty
	CmdBin   string // absolute path of the provider binary
	BasePath string // URL path prefix the proxy serves; the session id is appended
	User     string // who asked, for the audit log
	Env      []string
	Dir      string
}

// Session is one running gotty.
type Session struct {
	ID       string
	Type     provider.Type
	Name     string
	Command  string
	User     string
	BasePath string // ends in "/"
	Started  time.Time

	port       int
	credential string
	proc       *exec.Cmd
	release    func()
	lastActive atomic.Int64 // unix nanos
	wsTaken    atomic.Bool
	knownMu    sync.Mutex
	known      []procRef // last snapshot of the processes under gotty
	done       chan struct{}
	closeOnce  sync.Once
	reason     string
	m          *Manager
}

// Manager owns the open terminals.
type Manager struct {
	Wrap        WrapFunc
	IdleTimeout time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
	now      func() time.Time
	// readyTimeout bounds waiting for gotty to listen.
	readyTimeout time.Duration
}

// Default is the process-wide manager the HTTP layer uses.
var Default = NewManager()

// NewManager returns an empty manager.
func NewManager() *Manager {
	return &Manager{sessions: map[string]*Session{}, now: time.Now, readyTimeout: 10 * time.Second}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// freePort asks the kernel for an unused 127.0.0.1 port. gotty binds it a
// moment later; a race lost here fails the readiness wait, not silently.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start launches gotty for req and waits until it answers.
func (m *Manager) Start(req StartRequest) (*Session, error) {
	m.mu.Lock()
	if len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		return nil, ErrTooMany
	}
	m.mu.Unlock()

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	id := randHex(12)
	s := &Session{
		ID:         id,
		Type:       req.Instance.Type,
		Name:       req.Instance.Name,
		Command:    req.Command.Key,
		User:       req.User,
		BasePath:   req.BasePath + id + "/",
		Started:    m.now(),
		port:       port,
		credential: randHex(8) + ":" + randHex(24),
		done:       make(chan struct{}),
		m:          m,
	}
	s.touch()

	cfgDir, err := os.MkdirTemp("", "wick-gotty-")
	if err != nil {
		return nil, err
	}
	// gotty reads it once at startup; gone as soon as it serves.
	defer os.RemoveAll(cfgDir)
	cfgPath := filepath.Join(cfgDir, "gotty.hcl")
	if err := os.WriteFile(cfgPath, GottyConfig(s.credential), 0o600); err != nil {
		return nil, err
	}
	bin, args := req.GottyBin, GottyArgs(Spec{
		Port:       port,
		ConfigPath: cfgPath,
		BasePath:   s.BasePath,
		Bin:        req.CmdBin,
		Args:       req.Command.Args,
	})
	s.release = func() {}
	if m.Wrap != nil {
		bin, args, s.release = m.Wrap(bin, args)
	}
	cmd := safeexec.Command(bin, args...)
	cmd.Env = req.Env
	cmd.Dir = req.Dir
	setProcGroup(cmd)
	// gotty's own log names the credential ("Basic Authentication" only,
	// but keep it off wick's stdout regardless).
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		s.release()
		return nil, fmt.Errorf("start gotty: %w", err)
	}
	s.proc = cmd

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	log.Info().
		Str("component", "terminal").
		Str("event", "start").
		Str("session", id).
		Str("user", req.User).
		Str("type", string(s.Type)).
		Str("name", s.Name).
		Str("command", req.Command.Label).
		Int("pid", cmd.Process.Pid).
		Msg("terminal: session start")

	go s.waitLoop()
	go s.idleLoop()

	if err := s.waitReady(m.readyTimeout); err != nil {
		s.Close("start failed")
		return nil, err
	}
	return s, nil
}

// waitReady polls gotty's index until it answers 200.
func (s *Session) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cl := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-s.done:
			return errors.New("gotty exited before it was ready")
		default:
		}
		req, _ := http.NewRequest(http.MethodGet, s.upstream("http")+s.BasePath, nil)
		s.auth(req.Header)
		if resp, err := cl.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("gotty did not become ready in time")
}

func (s *Session) upstream(scheme string) string {
	return fmt.Sprintf("%s://127.0.0.1:%d", scheme, s.port)
}

// remember snapshots the processes under gotty (see killKnown).
func (s *Session) remember() {
	if pid := s.Pid(); pid > 0 {
		if refs := snapshot(pid); len(refs) > 0 {
			s.knownMu.Lock()
			s.known = refs
			s.knownMu.Unlock()
		}
	}
}

func (s *Session) touch() { s.lastActive.Store(time.Now().UnixNano()) }

// Done is closed once the session has ended and its processes are gone.
func (s *Session) Done() <-chan struct{} { return s.done }

// Pid is gotty's pid (the wrapper's, when memory-scoped).
func (s *Session) Pid() int {
	if s.proc == nil || s.proc.Process == nil {
		return 0
	}
	return s.proc.Process.Pid
}

func (s *Session) waitLoop() {
	_ = s.proc.Wait()
	s.Close("gotty exited")
}

func (s *Session) idleLoop() {
	idle := s.m.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	tick := idle / 10
	if tick > 30*time.Second {
		tick = 30 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.remember()
			if time.Since(time.Unix(0, s.lastActive.Load())) > idle {
				s.Close("idle")
				return
			}
		}
	}
}

// Close ends the session: gotty and everything under it are killed, the
// memory scope released, the session forgotten. Idempotent.
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		s.reason = reason
		if s.proc != nil && s.proc.Process != nil {
			killTree(s.proc.Process.Pid)
		}
		// Anything that outlived gotty and got reparented away.
		s.knownMu.Lock()
		killKnown(s.known)
		s.knownMu.Unlock()
		s.m.mu.Lock()
		delete(s.m.sessions, s.ID)
		s.m.mu.Unlock()
		if s.release != nil {
			s.release()
		}
		log.Info().
			Str("component", "terminal").
			Str("event", "end").
			Str("session", s.ID).
			Str("user", s.User).
			Str("type", string(s.Type)).
			Str("name", s.Name).
			Str("command", s.Command).
			Str("reason", reason).
			Dur("duration", time.Since(s.Started)).
			Msg("terminal: session end")
		close(s.done)
	})
}

// Get returns an open session by id, nil when there is none.
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// Count is the number of open sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Shutdown closes every open session (wick is stopping).
func (m *Manager) Shutdown(context.Context) error {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.Close("wick shutdown")
	}
	return nil
}

// HomeDir is the working directory a terminal starts in, like a login TTY.
func HomeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
