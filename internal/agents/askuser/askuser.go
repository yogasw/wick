// Package askuser holds the daemon-side coordinator for the
// ask_user MCP tool. The agent (claude / codex / gemini) calls
// `ask_user` over MCP; the handler registers a pending question +
// broadcasts SSE; the user answers in the web UI; the answer
// resolves the pending channel, and the MCP tool returns to the
// agent.
//
// Lifecycle parallels gate.ApprovalManager but the trigger is an
// MCP RPC instead of a unix socket dial — there is no subprocess
// hook, just a blocking goroutine inside the wick process.
package askuser

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultTimeout is how long Ask blocks before returning an error
// to the calling agent. Five minutes is long enough for "the user
// stepped away to grab coffee" but short enough that a forgotten
// session doesn't pin a goroutine forever. Configurable per Ask
// via Question.Timeout.
const DefaultTimeout = 5 * time.Minute

// Option is one choice presented to the user. Label = what they
// see, Value = what gets returned to the agent.
type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Question is the input to Manager.Ask. Mirrors the MCP tool's
// input schema 1:1 so the handler can pass it through unchanged.
type Question struct {
	SessionID     string        `json:"session_id"`
	AgentName     string        `json:"agent_name,omitempty"`
	Question      string        `json:"question"`
	Options       []Option      `json:"options,omitempty"`
	AllowFreeform bool          `json:"allow_freeform,omitempty"`
	Timeout       time.Duration `json:"-"`
}

// Answer is what the user posts via /sessions/{id}/answer. One of
// Value / Text is set; if both are present, Value wins (it's the
// label of a clicked option, more authoritative than free-typed
// text on the same form).
type Answer struct {
	Value string `json:"value,omitempty"`
	Text  string `json:"text,omitempty"`
}

// AskRequest is the broadcast payload — Question + a server-minted
// id used by both UI (in POST /answer) and the manager's pending
// map (to route the answer back).
type AskRequest struct {
	ID            string   `json:"id"`
	SessionID     string   `json:"session_id"`
	AgentName     string   `json:"agent_name,omitempty"`
	Question      string   `json:"question"`
	Options       []Option `json:"options,omitempty"`
	AllowFreeform bool     `json:"allow_freeform,omitempty"`
}

// pending is one in-flight ask. ch is buffered cap 1 so a late
// resolver doesn't block; cancel handles the agent giving up
// (request context cancelled / MCP transport closed).
type pending struct {
	req AskRequest
	ch  chan Answer
}

// Manager owns the per-process pending-asks map. Concurrent-safe;
// caller is expected to wire OnRequest/OnResolved into the SSE
// broadcaster so the UI sees state changes.
type Manager struct {
	defaultTimeout time.Duration
	onRequest      func(AskRequest)
	onResolved     func(sessionID, requestID string)

	mu      sync.Mutex
	pending map[string]*pending // requestID → pending
}

// Options wires callbacks. Both are optional; nil = no broadcast.
type Options struct {
	DefaultTimeout time.Duration
	OnRequest      func(AskRequest)
	OnResolved     func(sessionID, requestID string)
}

// NewManager constructs an empty manager.
func NewManager(opt Options) *Manager {
	t := opt.DefaultTimeout
	if t <= 0 {
		t = DefaultTimeout
	}
	return &Manager{
		defaultTimeout: t,
		onRequest:      opt.OnRequest,
		onResolved:     opt.OnResolved,
		pending:        make(map[string]*pending),
	}
}

// Ask registers a pending question, fires onRequest, and blocks
// until the user answers, the timeout fires, or done is closed.
//
// Returns Answer + error. error is non-nil only on timeout / cancel
// — agent-side handlers convert that into a tool error so the LLM
// can decide to retry or give up.
func (m *Manager) Ask(q Question, done <-chan struct{}) (Answer, error) {
	if q.SessionID == "" {
		return Answer{}, errors.New("askuser: SessionID required")
	}
	if q.Question == "" {
		return Answer{}, errors.New("askuser: Question required")
	}
	timeout := q.Timeout
	if timeout <= 0 {
		timeout = m.defaultTimeout
	}

	id := mintID()
	req := AskRequest{
		ID:            id,
		SessionID:     q.SessionID,
		AgentName:     q.AgentName,
		Question:      q.Question,
		Options:       q.Options,
		AllowFreeform: q.AllowFreeform,
	}
	ch := make(chan Answer, 1)
	m.mu.Lock()
	m.pending[id] = &pending{req: req, ch: ch}
	m.mu.Unlock()

	if m.onRequest != nil {
		// Run in a goroutine so a slow handler can't stall Ask.
		go m.onRequest(req)
	}

	defer func() {
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
		if m.onResolved != nil {
			m.onResolved(q.SessionID, id)
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case ans := <-ch:
		return ans, nil
	case <-timer.C:
		return Answer{}, fmt.Errorf("askuser: user did not respond within %s", timeout)
	case <-done:
		return Answer{}, errors.New("askuser: cancelled by caller")
	}
}

// Resolve delivers an answer to the matching pending Ask. Returns
// false if the id is unknown — typical when the agent gave up or
// the request already timed out.
func (m *Manager) Resolve(requestID string, ans Answer) bool {
	m.mu.Lock()
	p, ok := m.pending[requestID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	delete(m.pending, requestID)
	m.mu.Unlock()
	select {
	case p.ch <- ans:
		return true
	default:
		return false
	}
}

// PendingFor returns a snapshot of in-flight asks for one session.
// Used by the UI for reconnect rehydrate.
func (m *Manager) PendingFor(sessionID string) []AskRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AskRequest, 0)
	for _, p := range m.pending {
		if p.req.SessionID == sessionID {
			out = append(out, p.req)
		}
	}
	return out
}

func mintID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
