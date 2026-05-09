package pool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// Pool is the global slot manager. It tracks how many agent
// subprocesses are alive across all sessions, FIFO-queues sessions
// that arrive while full, and grants slots when one frees up.
//
// Pool deliberately knows nothing about CLI specifics — it asks an
// AgentFactory to build an *provider.Agent for a given session+agent
// name. Tests inject a factory that returns agents wired to the
// fakeSpawner; production wires ClaudeSpawner.
type Pool struct {
	cfg PoolConfig

	mu           sync.Mutex
	active       map[string]*runEntry // key = sessionKey(sessionID, agentName)
	spawningKeys map[string]struct{}  // sessions mid-spawn: slot reserved, not yet in active
	queue        []queueEntry
	buffers      map[string]*Buffer // per-session buffer, lazily created
	closed       bool

	// wg tracks tryGrantQueue background spawns + onAgentExit work so
	// Stop can wait for all post-exit disk writes (markStatus, queue
	// drain) to finish before returning. Without this, tests that
	// observe Active==0 race the trailing meta.json writes.
	wg sync.WaitGroup
}

// PoolConfig knobs.
//
// DefaultWorkspace is the workspace name used when a session has no
// workspace bound. Empty = no default; the pool falls back to a
// per-session temp dir so claude still has a stable cwd. See
// agents-design.md §0.2 D4.
type PoolConfig struct {
	MaxConcurrent    int
	IdleTimeout      time.Duration
	Layout           config.Layout
	Factory          AgentFactory
	DefaultWorkspace string
}

// AgentFactory builds an agent ready to Start. The pool wires the
// OnExit hook itself (so it can free the slot); the factory should
// not.
type AgentFactory interface {
	Build(opt FactoryOptions) (*provider.Agent, *state.Machine, *store.Store, error)
}

// FactoryOptions is what the pool hands to the factory. ResumeID is
// pulled from the session's agents.json by the pool. ProviderType /
// ProviderName identify which provider runtime instance to spawn
// against — empty ProviderName resolves to the per-type default whose
// name equals the type itself ("claude" / "codex" / "gemini"). Both
// are forwarded to the spawn logger so /tools/agents/providers can
// surface per-provider history without re-parsing files.
type FactoryOptions struct {
	SessionID    string
	AgentName    string
	ProviderType string
	ProviderName string
	Workspace    string
	ResumeID     string
	IdleTimeout  time.Duration
	OnEvent      func(event.AgentEvent)
}

// queueEntry is one request waiting for a slot.
type queueEntry struct {
	sessionID string
	agentName string
	enqueued  time.Time
}

// runEntry tracks an active agent in the pool.
type runEntry struct {
	agent    *provider.Agent
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
		cfg:          cfg,
		active:       map[string]*runEntry{},
		spawningKeys: map[string]struct{}{},
		buffers:      map[string]*Buffer{},
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
	// If this session is already mid-spawn, the in-flight spawn's Drain
	// will pick up the buffered message — nothing more to do here.
	if _, spawning := p.spawningKeys[key]; spawning {
		p.mu.Unlock()
		return nil
	}
	// Count in-flight spawns against the cap so concurrent Sends cannot
	// each see "slot free" and all call spawn simultaneously.
	if len(p.active)+len(p.spawningKeys) < p.cfg.MaxConcurrent {
		p.spawningKeys[key] = struct{}{}
		p.mu.Unlock()
		err := p.spawn(ctx, sessionID, agentName, source)
		p.mu.Lock()
		delete(p.spawningKeys, key)
		p.mu.Unlock()
		return err
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
	pType := ""
	for _, a := range sess.Agents {
		if a.Name == agentName {
			resumeID = a.CLISessionID
			pType = a.Provider
			break
		}
	}

	cwd, err := p.resolveCwd(sess)
	if err != nil {
		return err
	}

	a, st, sto, err := p.cfg.Factory.Build(FactoryOptions{
		SessionID:    sessionID,
		AgentName:    agentName,
		ProviderType: pType,
		ProviderName: pType, // default-name = type until per-instance pickers ship
		Workspace:    cwd,
		ResumeID:     resumeID,
		IdleTimeout:  p.cfg.IdleTimeout,
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
	if p.closed || len(p.queue) == 0 {
		p.mu.Unlock()
		return
	}
	// Count in-flight spawns — same as Send — so two concurrent exit
	// hooks cannot both see "slot free" and each pop from the queue.
	if len(p.active)+len(p.spawningKeys) >= p.cfg.MaxConcurrent {
		p.mu.Unlock()
		return
	}
	q := p.queue[0]
	p.queue = p.queue[1:]
	key := sessionKey(q.sessionID, q.agentName)
	p.spawningKeys[key] = struct{}{}
	p.wg.Add(1)
	p.mu.Unlock()
	// Background spawn — don't block whoever fired the exit hook.
	go func() {
		defer p.wg.Done()
		_ = p.spawn(context.Background(), q.sessionID, q.agentName, "queue")
		p.mu.Lock()
		delete(p.spawningKeys, key)
		p.mu.Unlock()
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

// resolveCwd determines the spawn cwd for a session. The fallback
// chain (agents-design.md §0.2 D4):
//
//  1. session.Meta.Workspace — explicit binding
//  2. PoolConfig.DefaultWorkspace — tools-config default
//  3. <BaseDir>/sessions/<id>/cwd/ — per-session temp dir
//
// For named workspaces (steps 1-2) the returned path is the
// workspace's resolved cwd (managed `files/` or custom path). The
// pool MkdirAll's managed paths so the spawn never fails on a
// missing directory; custom paths are assumed to exist (validated
// at workspace create time).
func (p *Pool) resolveCwd(sess session.Session) (string, error) {
	name := sess.Meta.Workspace
	if name == "" {
		name = p.cfg.DefaultWorkspace
	}
	if name != "" {
		path, err := workspace.ResolvePath(p.cfg.Layout, name)
		if err != nil {
			return "", fmt.Errorf("resolve workspace %q: %w", name, err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("ensure workspace path %q: %w", path, err)
		}
		return path, nil
	}
	fallback := filepath.Join(p.cfg.Layout.SessionDir(sess.ID), "cwd")
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", fmt.Errorf("ensure session fallback cwd %q: %w", fallback, err)
	}
	return fallback, nil
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

// MaxConcurrent surfaces the configured slot cap for the Backends UI.
// Read-only — change via PoolConfig at construction time.
func (p *Pool) MaxConcurrent() int {
	return p.cfg.MaxConcurrent
}

// QueueSnapshot returns a defensive copy of the current FIFO queue
// (oldest first). Used by the Backends UI to show what's waiting.
func (p *Pool) QueueSnapshot() []QueueEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]QueueEntry, len(p.queue))
	for i, q := range p.queue {
		out[i] = QueueEntry{
			SessionID: q.sessionID,
			AgentName: q.agentName,
			Enqueued:  q.enqueued,
		}
	}
	return out
}

// QueueEntry is the public snapshot view of one queued request.
type QueueEntry struct {
	SessionID string
	AgentName string
	Enqueued  time.Time
}

// ActiveSnapshot returns a defensive copy of every running agent in
// the pool. Used by the Backends UI to show what's eating each slot.
func (p *Pool) ActiveSnapshot() []ActiveEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ActiveEntry, 0, len(p.active))
	for _, e := range p.active {
		out = append(out, ActiveEntry{
			SessionID: e.sessID,
			AgentName: e.agentNm,
		})
	}
	return out
}

// ActiveEntry is the public snapshot view of one running agent.
type ActiveEntry struct {
	SessionID string
	AgentName string
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
func (p *Pool) HandleExit(sessionID, agentName string, _ provider.ExitReason) {
	p.onAgentExit(sessionID, agentName)
}

// sessionKey is the canonical map key for an active agent.
func sessionKey(sessionID, agentName string) string {
	return sessionID + "::" + agentName
}
