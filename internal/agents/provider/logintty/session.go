package logintty

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
)

// TTL policy for login sessions: start at DefaultTTL, each extend adds
// ExtendStep, and the session never lives past MaxTTL from start.
const (
	DefaultTTL = 5 * time.Minute
	ExtendStep = 5 * time.Minute
	MaxTTL     = 30 * time.Minute
)

// replayCap bounds how much output a session keeps for late attachers.
const replayCap = 256 << 10

// Session states.
const (
	StateRunning = "running"
	StateExited  = "exited"
	StateExpired = "expired"
	StateKilled  = "killed"
)

// Frame is one JSON message on the login TTY websocket (server →
// client). T discriminates; the other fields are per-type payload.
type Frame struct {
	T          string   `json:"t"`              // out | link | success | fail | ttl | state
	Data       string   `json:"data,omitempty"` // base64 PTY output (t=out)
	URL        string   `json:"url,omitempty"`  // t=link
	Line       string   `json:"line,omitempty"` // t=success
	RemainingS int      `json:"remaining_s"`    // t=ttl: seconds until kill
	CapS       int      `json:"cap_s"`          // t=ttl: seconds until the hard cap
	State      string   `json:"state,omitempty"`
	ExitErr    string   `json:"exit_err,omitempty"`
	Account    *Account `json:"account,omitempty"` // t=state, after exit
}

// runner abstracts the PTY-attached login process so the manager is
// testable without a real ConPTY: Read streams PTY output, Write
// feeds user keystrokes in.
type runner interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Kill() error
	Wait() error
}

type spawnFunc func(bin string, args, env []string, cols, rows int) (runner, error)

// Default PTY geometry until the browser terminal reports its real
// size via a resize frame.
const (
	defaultCols = 120
	defaultRows = 32
)

// Session is one live (or recently finished) login TTY for a provider
// instance.
type Session struct {
	ID   string
	Type provider.Type
	Name string
	Bin  string
	Args []string

	env       []string // instance env, for the post-exit account probe
	countdown *Countdown
	run       runner

	mu       sync.Mutex
	parser   LinkParser
	buf      []byte
	links    []string
	success  string
	failure  string
	state    string
	exitErr  string
	account  *Account
	subs     map[chan Frame]struct{}
	lastTTLs int // last broadcast remaining, skip duplicate ttl frames

	done chan struct{} // closed after the exit state is broadcast
}

// Manager owns at most one login session per provider instance.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	spawn    spawnFunc
	now      func() time.Time
}

// NewManager returns a production manager that spawns real PTYs.
func NewManager() *Manager { return newManagerWith(ptySpawn, time.Now) }

// newManagerWith injects the spawn function and clock — the test
// seam.
func newManagerWith(spawn spawnFunc, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{sessions: map[string]*Session{}, spawn: spawn, now: now}
}

func sessionKey(t provider.Type, name string) string { return string(t) + "/" + name }

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start launches (or returns the already-running) login session for
// one instance. bin is the resolved binary path.
func (m *Manager) Start(ins provider.Instance, bin string) (*Session, error) {
	args, ok := LoginCommand(ins.Type, ins.ExtraArgs)
	if !ok {
		return nil, fmt.Errorf("tty login is not supported for provider type %q yet", ins.Type)
	}

	key := sessionKey(ins.Type, ins.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[key]; existing != nil && existing.State() == StateRunning {
		return existing, nil
	}

	env := append(append(os.Environ(), ins.Env...), LoginEnv(ins.Type)...)
	run, err := m.spawn(bin, args, env, defaultCols, defaultRows)
	if err != nil {
		return nil, fmt.Errorf("spawn login tty: %w", err)
	}

	s := &Session{
		ID:        newSessionID(),
		Type:      ins.Type,
		Name:      ins.Name,
		Bin:       bin,
		Args:      args,
		env:       append([]string{}, ins.Env...),
		countdown: NewCountdown(DefaultTTL, MaxTTL, m.now),
		run:       run,
		parser:    LinkParser{Cols: defaultCols},
		state:     StateRunning,
		subs:      map[chan Frame]struct{}{},
		done:      make(chan struct{}),
	}
	m.sessions[key] = s

	l := log.With().
		Str("component", "logintty").
		Str("type", string(ins.Type)).
		Str("name", ins.Name).
		Str("session", s.ID).
		Logger()
	l.Info().Str("bin", bin).Strs("args", args).Msg("logintty: session start")

	go s.readLoop()
	go s.waitLoop()
	go s.ttlLoop()
	return s, nil
}

// Get returns the current session for an instance, or nil.
func (m *Manager) Get(t provider.Type, name string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionKey(t, name)]
}

// Extend pushes the session's kill deadline out by ExtendStep. Returns
// the new ttl frame and false when there is no running session or the
// cap is reached.
func (m *Manager) Extend(t provider.Type, name string) (Frame, bool) {
	s := m.Get(t, name)
	if s == nil || s.State() != StateRunning {
		return Frame{}, false
	}
	ok := s.countdown.Extend(ExtendStep)
	fr := s.TTLFrame()
	if ok {
		s.broadcast(fr)
	}
	return fr, ok
}

// Kill terminates the session's process (state → killed).
func (m *Manager) Kill(t provider.Type, name string) bool {
	s := m.Get(t, name)
	if s == nil || s.State() != StateRunning {
		return false
	}
	s.setState(StateKilled)
	_ = s.run.Kill()
	return true
}

// State returns the session's current lifecycle state.
func (s *Session) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// AccountSnapshot returns the post-exit account probe result, nil
// while running.
func (s *Session) AccountSnapshot() *Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

// setState flips the lifecycle state only while still running — the
// first terminal state (expired/killed/exited) wins.
func (s *Session) setState(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateRunning {
		return false
	}
	s.state = state
	return true
}

// TTLFrame snapshots the countdown into a ttl frame.
func (s *Session) TTLFrame() Frame {
	remaining := int(s.countdown.Remaining().Seconds())
	return Frame{T: "ttl", RemainingS: remaining, CapS: capSeconds(s.countdown)}
}

func capSeconds(c *Countdown) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	left := c.max.Sub(c.now())
	if left < 0 {
		return 0
	}
	return int(left.Seconds())
}

// Attach subscribes ch to session frames. The returned snapshot
// replays what a late joiner needs: buffered output, links found so
// far, current ttl and state. Call Detach(ch) when done.
func (s *Session) Attach(ch chan Frame) []Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	var snap []Frame
	if len(s.buf) > 0 {
		snap = append(snap, Frame{T: "out", Data: base64.StdEncoding.EncodeToString(s.buf)})
	}
	for _, u := range s.links {
		snap = append(snap, Frame{T: "link", URL: u})
	}
	if s.success != "" {
		snap = append(snap, Frame{T: "success", Line: s.success})
	}
	if s.failure != "" {
		snap = append(snap, Frame{T: "fail", Line: s.failure})
	}
	snap = append(snap, Frame{T: "ttl", RemainingS: int(s.countdown.Remaining().Seconds()), CapS: capSeconds(s.countdown)})
	st := Frame{T: "state", State: s.state, ExitErr: s.exitErr}
	if s.account != nil {
		st.Account = s.account
	}
	snap = append(snap, st)
	s.subs[ch] = struct{}{}
	return snap
}

// Detach removes a subscriber.
func (s *Session) Detach(ch chan Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, ch)
}

// broadcast fans a frame out to all subscribers, dropping for slow
// ones — the replay buffer covers them on reattach.
func (s *Session) broadcast(fr Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(fr)
}

func (s *Session) broadcastLocked(fr Frame) {
	for ch := range s.subs {
		select {
		case ch <- fr:
		default:
		}
	}
}

// Input writes user keystrokes to the PTY.
func (s *Session) Input(p []byte) error {
	_, err := s.run.Write(p)
	return err
}

// Resize resizes the PTY and updates the parser's wrap-join width.
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid size")
	}
	s.mu.Lock()
	s.parser.Cols = cols
	s.mu.Unlock()
	return s.run.Resize(cols, rows)
}

// Done is closed once the session reached a terminal state and its
// exit frame was broadcast.
func (s *Session) Done() <-chan struct{} { return s.done }

// readLoop streams PTY output: buffers it for replay, tees it through
// the link parser, and broadcasts frames.
func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.run.Read(buf)
		if n > 0 {
			chunk := append([]byte{}, buf[:n]...)
			s.mu.Lock()
			s.buf = append(s.buf, chunk...)
			if len(s.buf) > replayCap {
				s.buf = append([]byte{}, s.buf[len(s.buf)-replayCap:]...)
			}
			evs := s.parser.Feed(chunk)
			s.broadcastLocked(Frame{T: "out", Data: base64.StdEncoding.EncodeToString(chunk)})
			for _, ev := range evs {
				switch ev.Type {
				case EventLink:
					s.links = append(s.links, ev.Value)
					s.broadcastLocked(Frame{T: "link", URL: ev.Value})
				case EventSuccess:
					s.success = ev.Value
					s.broadcastLocked(Frame{T: "success", Line: ev.Value})
				case EventFailure:
					s.failure = ev.Value
					s.broadcastLocked(Frame{T: "fail", Line: ev.Value})
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Debug().Str("component", "logintty").Str("session", s.ID).Err(err).Msg("logintty: read loop end")
			}
			return
		}
	}
}

// waitLoop reaps the process, runs the account probe, and broadcasts
// the terminal state frame.
func (s *Session) waitLoop() {
	err := s.run.Wait()
	// Expired/killed states were already set by their initiators; a
	// natural exit lands here first.
	s.setState(StateExited)

	acc := ReadAccount(s.Type, s.env)

	s.mu.Lock()
	if err != nil {
		s.exitErr = err.Error()
	}
	s.account = &acc
	fr := Frame{T: "state", State: s.state, ExitErr: s.exitErr, Account: s.account}
	s.broadcastLocked(fr)
	s.mu.Unlock()

	log.Info().
		Str("component", "logintty").
		Str("session", s.ID).
		Str("state", s.State()).
		Bool("connected", acc.Connected).
		Str("email", acc.Email).
		Msg("logintty: session end")
	close(s.done)
}

// ttlLoop broadcasts the countdown and enforces expiry once per
// second until the session ends.
func (s *Session) ttlLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-tick.C:
			s.enforceTTL()
			if s.State() != StateRunning {
				return
			}
			fr := s.TTLFrame()
			s.mu.Lock()
			if fr.RemainingS != s.lastTTLs {
				s.lastTTLs = fr.RemainingS
				s.broadcastLocked(fr)
			}
			s.mu.Unlock()
		}
	}
}

// enforceTTL kills the process when the countdown has expired. Called
// by the ticker loop; exposed for tests with a fake clock.
func (s *Session) enforceTTL() {
	if !s.countdown.Expired() {
		return
	}
	if s.setState(StateExpired) {
		log.Info().Str("component", "logintty").Str("session", s.ID).Msg("logintty: ttl expired, killing")
		_ = s.run.Kill()
	}
}
