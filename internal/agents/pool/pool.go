package pool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/agent"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

// Pool is the global slot manager. It tracks how many agent
// subprocesses are alive across all sessions, FIFO-queues sessions
// that arrive while full, and grants slots when one frees up.
//
// Pool deliberately knows nothing about CLI specifics — it asks an
// AgentFactory to build an *agent.Agent for a given session+agent
// name. Tests inject a factory that returns agents wired to the
// fakeSpawner; production wires ClaudeSpawner.
type Pool struct {
	cfg PoolConfig

	mu      sync.Mutex
	active  map[string]*runEntry // key = sessionKey(sessionID, agentName)
	queue   []queueEntry
	buffers map[string]*Buffer // per-session buffer, lazily created
	closed  bool

	// wg tracks tryGrantQueue background spawns + onAgentExit work so
	// Stop can wait for all post-exit disk writes (markStatus, queue
	// drain) to finish before returning. Without this, tests that
	// observe Active==0 race the trailing meta.json writes.
	wg sync.WaitGroup
}

// PoolConfig knobs.
type PoolConfig struct {
	MaxConcurrent int
	IdleTimeout   time.Duration
	Layout        config.Layout
	Factory       AgentFactory
}

// AgentFactory builds an agent ready to Start. The pool wires the
// OnExit hook itself (so it can free the slot); the factory should
// not.
type AgentFactory interface {
	Build(opt FactoryOptions) (*agent.Agent, *state.Machine, *store.Store, error)
}

// FactoryOptions is what the pool hands to the factory. ResumeID is
// pulled from the session's agents.json by the pool.
type FactoryOptions struct {
	SessionID   string
	AgentName   string
	Workspace   string
	ResumeID    string
	IdleTimeout time.Duration
	OnEvent     func(event.AgentEvent)
}

// queueEntry is one request waiting for a slot.
type queueEntry struct {
	sessionID string
	agentName string
	enqueued  time.Time
}

// runEntry tracks an active agent in the pool.
type runEntry struct {
	agent    *agent.Agent
	state    *state.Machine
	store    *store.Store
	buffer   *Buffer
	sessID   string
	agentNm  string
}

// New returns an empty pool.
func New(cfg PoolConfig) *Pool {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 120 * time.Second
	}
	return &Pool{
		cfg:     cfg,
		active:  map[string]*runEntry{},
		buffers: map[string]*Buffer{},
	}
}

// Send routes a user message into the right session. If a slot is
// free the agent is spawned and the message sent immediately; else
// the message is appended to the session's buffer and the request is
// queued. The on-disk session meta status is updated to reflect
// running/queued so UI listings stay correct.
func (p *Pool) Send(ctx context.Context, sessionID, agentName, source, role, text string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("pool closed")
	}
	key := sessionKey(sessionID, agentName)
	entry, alive := p.active[key]
	p.mu.Unlock()

	if alive {
		// Active agent — append to conversation log + send straight.
		if entry.store != nil {
			_ = entry.store.AppendUserTurn(role, source, text)
		}
		return entry.agent.Send(text)
	}

	// Not active. Buffer the message and either spawn (slot free) or
	// queue (pool full).
	buf, err := p.bufferFor(sessionID)
	if err != nil {
		return err
	}
	if err := buf.Append(text); err != nil {
		return err
	}

	p.mu.Lock()
	if len(p.active) < p.cfg.MaxConcurrent {
		p.mu.Unlock()
		return p.spawn(ctx, sessionID, agentName, source)
	}
	// Full — queue the request and update session status.
	p.queue = append(p.queue, queueEntry{sessionID, agentName, time.Now()})
	p.mu.Unlock()
	return p.markStatus(sessionID, session.StatusQueued)
}

// spawn allocates a slot, builds an agent via the factory, drains the
// buffer into one combined input, and starts it. Caller must NOT hold
// p.mu (we acquire and release it ourselves so the spawn can take
// time without blocking other Send calls).
func (p *Pool) spawn(ctx context.Context, sessionID, agentName, source string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("pool closed")
	}
	p.mu.Unlock()
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return err
	}
	resumeID := ""
	for _, a := range sess.Agents {
		if a.Name == agentName {
			resumeID = a.CLISessionID
			break
		}
	}

	a, st, sto, err := p.cfg.Factory.Build(FactoryOptions{
		SessionID:   sessionID,
		AgentName:   agentName,
		Workspace:   p.cfg.Layout.SessionWorkspace(sessionID),
		ResumeID:    resumeID,
		IdleTimeout: p.cfg.IdleTimeout,
	})
	if err != nil {
		return err
	}
	// Wire OnExit so the pool reclaims the slot.
	key := sessionKey(sessionID, agentName)
	entry := &runEntry{
		agent:   a,
		state:   st,
		store:   sto,
		sessID:  sessionID,
		agentNm: agentName,
	}
	p.mu.Lock()
	if p.closed {
		// Stop raced ahead of us — bail before publishing the entry,
		// otherwise its later idle exit would call wg.Add after Stop's
		// wg.Wait has returned.
		p.mu.Unlock()
		return errors.New("pool closed")
	}
	if buf, ok := p.buffers[sessionID]; ok {
		entry.buffer = buf
	}
	p.active[key] = entry
	p.mu.Unlock()

	// Drain the buffer into one combined input — design §5.1.1.
	combined, err := entry.buffer.Drain()
	if err != nil {
		return err
	}

	if err := p.markStatus(sessionID, session.StatusRunning); err != nil {
		return err
	}
	if err := a.Start(ctx); err != nil {
		p.releaseSlot(key)
		return err
	}
	if combined != "" {
		if entry.store != nil {
			_ = entry.store.AppendUserTurn("user", source, combined)
		}
		if err := a.Send(combined); err != nil {
			return err
		}
	}
	return nil
}

// onAgentExit is the hook the factory wires for us. The pool marks
// the session idle, releases the slot, and tries to grant the slot to
// the next queued session.
//
// Order matters: markStatus must run BEFORE releaseSlot so that any
// caller observing Active==0 also sees the on-disk Status=idle. With
// the reverse order, a fast Send-after-Active==0 races the trailing
// meta.json write and can collide with its own meta writes on Windows
// (os.Rename to the same target from two goroutines).
//
// The whole body runs under p.wg so Stop() can wait for any tail
// work to finish before tearing down.
func (p *Pool) onAgentExit(sessionID, agentName string) {
	p.wg.Add(1)
	defer p.wg.Done()
	key := sessionKey(sessionID, agentName)
	_ = p.markStatus(sessionID, session.StatusIdle)
	p.releaseSlot(key)
	p.tryGrantQueue()
}

func (p *Pool) releaseSlot(key string) {
	p.mu.Lock()
	delete(p.active, key)
	p.mu.Unlock()
}

// tryGrantQueue pops the head of the queue and spawns it if a slot is
// free. Runs every time a slot is released. Skips work entirely if the
// pool is closed — Stop() relies on this so no fresh spawns sneak past
// the shutdown barrier.
func (p *Pool) tryGrantQueue() {
	p.mu.Lock()
	if p.closed || len(p.queue) == 0 || len(p.active) >= p.cfg.MaxConcurrent {
		p.mu.Unlock()
		return
	}
	q := p.queue[0]
	p.queue = p.queue[1:]
	p.wg.Add(1)
	p.mu.Unlock()
	// Background spawn — don't block whoever fired the exit hook.
	go func() {
		defer p.wg.Done()
		_ = p.spawn(context.Background(), q.sessionID, q.agentName, "queue")
	}()
}

// bufferFor returns or lazy-creates the per-session Buffer.
func (p *Pool) bufferFor(sessionID string) (*Buffer, error) {
	p.mu.Lock()
	if b, ok := p.buffers[sessionID]; ok {
		p.mu.Unlock()
		return b, nil
	}
	p.mu.Unlock()
	buf, err := NewBuffer(p.cfg.Layout, sessionID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if existing, ok := p.buffers[sessionID]; ok {
		// Race: another caller created one. Use theirs.
		p.mu.Unlock()
		return existing, nil
	}
	p.buffers[sessionID] = buf
	p.mu.Unlock()
	return buf, nil
}

// markStatus updates session meta.Status + LastActive.
func (p *Pool) markStatus(sessionID string, status session.Status) error {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return err
	}
	if sess.Meta.Status == status {
		return nil
	}
	sess.Meta.Status = status
	sess.Meta.LastActive = time.Now().UTC()
	return session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta)
}

// Stop tears down all active agents and waits for trailing
// post-exit work (markStatus, queue drain). Used on graceful shutdown
// and by tests to flush goroutines before TempDir cleanup.
func (p *Pool) Stop() {
	p.mu.Lock()
	p.closed = true
	entries := make([]*runEntry, 0, len(p.active))
	for _, e := range p.active {
		entries = append(entries, e)
	}
	p.mu.Unlock()
	for _, e := range entries {
		_ = e.agent.Stop()
	}
	p.wg.Wait()
}

// Active returns the number of running agents.
func (p *Pool) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active)
}

// QueueLen returns the number of queued requests.
func (p *Pool) QueueLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}

// Kill stops the running agent for sessionID+agentName. Idempotent if
// the agent is not currently active — returns nil in that case.
// The normal onAgentExit hook still fires, releasing the slot and
// draining the queue.
func (p *Pool) Kill(sessionID, agentName string) error {
	p.mu.Lock()
	key := sessionKey(sessionID, agentName)
	entry, ok := p.active[key]
	p.mu.Unlock()
	if !ok {
		return nil
	}
	return entry.agent.Stop()
}

// HandleExit is the public hook the factory wires into agent.OnExit.
// It defers to the unexported onAgentExit but accepts the reason so
// future code can branch (e.g. don't grant queue if the previous exit
// was an error).
func (p *Pool) HandleExit(sessionID, agentName string, _ agent.ExitReason) {
	p.onAgentExit(sessionID, agentName)
}

// sessionKey is the canonical map key for an active agent.
func sessionKey(sessionID, agentName string) string {
	return sessionID + "::" + agentName
}
