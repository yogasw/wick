package pool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
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
	stopCh       chan struct{} // closed by Stop to unwind background loops

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
	KillAfterIdle    time.Duration
	// PreemptIdle, when true, lets a queued send kick out the longest-idle
	// active subprocess (Lifecycle == Idle) so the new session doesn't have
	// to wait for the idle TTL. The preempted session keeps its CLI session
	// ID in agents.json and resumes via --resume on its next message.
	PreemptIdle      bool
	Layout           config.Layout
	Factory          AgentFactory
	DefaultWorkspace string
	// OnSessionCreated is called after the pool auto-creates a session for a
	// channel message (e.g. Slack thread_ts). Wire this to
	// manager.Register so the dashboard sees the session immediately.
	OnSessionCreated func(s session.Session)
	// OnLifecycle fires when the pool transitions a session+agent's
	// lifecycle (Spawning, Killed). Idle/Working transitions are
	// implicit from event flow and are NOT routed here — UIs that
	// want every transition should subscribe to AgentEvent via
	// the factory's OnEvent. Optional; nil = no callback.
	OnLifecycle func(LifecycleEvent)
}

// LifecycleEvent is emitted for the two transitions the pool drives
// directly (no parser event triggers them): a fresh spawn coming
// online, or a subprocess dying. PID is populated for spawning →
// working; 0 for killed.
type LifecycleEvent struct {
	SessionID string
	AgentName string
	Lifecycle string // "spawning" | "killed"
	PID       int
	At        time.Time
}

// AgentFactory builds an agent ready to Start. The pool wires the
// OnExit hook itself (so it can free the slot); the factory should
// not.
//
// BuildResult.OnStarted is called by the pool right after a.Start
// succeeds — that's when the OS pid is known and the first user
// message has been drained from the buffer. Factories use it to
// finish writing the spawn `start` event with both fields. Optional;
// nil = nothing to record.
type AgentFactory interface {
	Build(opt FactoryOptions) (BuildResult, error)
}

// BuildResult bundles everything Build returns. New code should pull
// fields from here; the bare-tuple shape is gone so we don't have to
// thread one more channel through every callsite when we add another
// hook later.
type BuildResult struct {
	Agent     *provider.Agent
	State     *state.Machine
	Store     *store.Store
	OnStarted func(meta SpawnStartMeta)
}

// SpawnStartMeta is the post-Start snapshot the pool feeds back to
// the factory so the spawn log gets a complete `start` record. PID,
// argv, and binary path are only knowable after Spawner.Spawn
// returns; FirstUserMessage comes from the buffer drain.
type SpawnStartMeta struct {
	PID              int
	Binary           string
	Argv             []string
	FirstUserMessage string
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
	ResumeID      string
	IdleTimeout   time.Duration
	KillAfterIdle time.Duration
	OnEvent       func(event.AgentEvent)
	// PresetName is the preset name from session meta. Factory resolves
	// the content from disk — pool passes the name so factory avoids a
	// redundant session.Load.
	PresetName string
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
	cwd      string // resolved workspace path (used by RouteByCWD)
}

// New returns an empty pool.
func New(cfg PoolConfig) *Pool {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 2
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 120 * time.Second
	}
	p := &Pool{
		cfg:          cfg,
		active:       map[string]*runEntry{},
		spawningKeys: map[string]struct{}{},
		buffers:      map[string]*Buffer{},
		stopCh:       make(chan struct{}),
	}
	if cfg.PreemptIdle {
		p.wg.Add(1)
		go p.preemptLoop()
	}
	return p
}

// preemptLoop periodically re-tries preemption while the queue is
// non-empty. Send() fires preempt once at enqueue time, but the active
// session may still have been Working then — by the time it transitions
// to Idle there's no signal back into the pool. The loop closes that
// gap: every second it checks if a queued session is still waiting and
// any active session has gone idle, and if so kicks the longest-idle
// victim. Cheap (one mutex peek per tick) and only runs when the
// operator opted in via PreemptIdle.
func (p *Pool) preemptLoop() {
	defer p.wg.Done()
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.mu.Lock()
			busy := p.closed || len(p.queue) == 0 || len(p.spawningKeys) > 0
			p.mu.Unlock()
			if busy {
				continue
			}
			p.preemptIdleSlot()
		}
	}
}

// Send routes a user message into the right session. If a slot is
// free the agent is spawned and the message sent immediately; else
// the message is appended to the session's buffer and the request is
// queued. The on-disk session meta status is updated to reflect
// running/queued so UI listings stay correct.
// SessionExists reports whether sessionID already has on-disk state.
// Cheap stat — no JSON parse. Used by channels (Slack, Telegram) to decide
// whether the next inbound message starts a brand-new session and needs
// a one-time origin-context turn injected before the user message.
//
// Implements channels.SessionChecker.
func (p *Pool) SessionExists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, err := os.Stat(p.cfg.Layout.SessionDir(sessionID))
	return err == nil
}

func (p *Pool) Send(ctx context.Context, sessionID, agentName, source, role, text string) error {
	return p.send(ctx, sessionID, agentName, source, role, text, "")
}

// SendWithWorkspace is like Send but binds sessionID to the named workspace
// when auto-creating the session. Pass an empty string for the default.
func (p *Pool) SendWithWorkspace(ctx context.Context, sessionID, agentName, source, role, text, workspace string) error {
	return p.send(ctx, sessionID, agentName, source, role, text, workspace)
}

func (p *Pool) send(ctx context.Context, sessionID, agentName, source, role, text, workspace string) error {
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
		if role == "user" {
			p.setLabelIfEmpty(sessionID, text)
		}
		return entry.agent.Send(text)
	}

	// Not active. Ensure the session exists on disk (channels like Slack
	// pass a thread_ts as the session ID; the session is never created
	// via the UI flow).
	if err := p.ensureSession(ctx, sessionID, source, workspace); err != nil {
		return err
	}
	// Set label before buf.Append so concurrent disk writes don't clobber PendingInput.
	if role == "user" {
		p.setLabelIfEmpty(sessionID, text)
	}

	// Buffer the message and either spawn (slot free) or queue (pool full).
	buf, err := p.bufferFor(sessionID)
	if err != nil {
		return err
	}
	if err := buf.Append(text); err != nil {
		return err
	}
	// Persist the user turn to conversation.jsonl immediately so a page
	// refresh while the session is buffered (queued or mid-spawn) still
	// shows the messages — they previously only lived in PendingInput.
	// We build a transient Store because no entry.store exists yet.
	p.persistBufferedTurn(sessionID, agentName, role, source, text)

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
	// Full — queue the request (dedup: one slot per session+agent; the
	// buffer carries any extra messages) and update session status.
	already := false
	for _, q := range p.queue {
		if q.sessionID == sessionID && q.agentName == agentName {
			already = true
			break
		}
	}
	if !already {
		p.queue = append(p.queue, queueEntry{sessionID, agentName, time.Now()})
	}
	p.mu.Unlock()
	if p.cfg.PreemptIdle {
		p.preemptIdleSlot()
	}
	return p.markStatus(sessionID, session.StatusQueued)
}

// persistBufferedTurn writes one user/system message to the session's
// conversation.jsonl from outside an active runEntry. The buffered path
// (subprocess not yet alive) used to skip this, which made messages
// disappear from the UI after a page refresh — they only lived in
// meta.PendingInput, which the conversation view doesn't read.
func (p *Pool) persistBufferedTurn(sessionID, agentName, role, source, text string) {
	sto := store.New(store.Options{
		Layout:    p.cfg.Layout,
		SessionID: sessionID,
		AgentName: agentName,
	})
	_ = sto.AppendUserTurn(role, source, text)
}

// preemptIdleSlot picks the longest-idle active entry (Lifecycle == Idle,
// oldest LastActive) and asynchronously Stops its agent so the slot is
// reclaimed for a queued session. Returns true if a victim was kicked.
//
// No-op when nothing matches: every active agent mid-turn, a spawn is
// already in flight (slot lands soon anyway), or the pool is closed.
// Caller must NOT hold p.mu. The Stop is best-effort and idempotent —
// concurrent preempts that pick the same victim simply double-call Stop.
//
// The victim keeps its CLI session ID on disk; its next inbound message
// triggers a respawn with --resume so the conversation continues.
func (p *Pool) preemptIdleSlot() bool {
	p.mu.Lock()
	if p.closed || len(p.spawningKeys) > 0 {
		p.mu.Unlock()
		return false
	}
	var victim *runEntry
	var oldest time.Time
	for _, e := range p.active {
		if e.state == nil || e.state.Lifecycle() != state.LifecycleIdle {
			continue
		}
		t := e.state.LastActive()
		if victim == nil || t.Before(oldest) {
			victim = e
			oldest = t
		}
	}
	p.mu.Unlock()
	if victim == nil {
		return false
	}
	log.Debug().Str("session", victim.sessID).Str("agent", victim.agentNm).
		Msg("pool: preempting idle slot for queued session")
	go func() { _ = victim.agent.Stop() }()
	return true
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
	// Ensure the agent entry exists in agents.json before reading it.
	// Channels like Slack auto-create sessions without going through the
	// UI flow that would call AddAgent, so agents.json stays [] and
	// CLISessionID is never written — breaking --resume on respawn.
	hasEntry := false
	for _, a := range sess.Agents {
		if a.Name == agentName {
			hasEntry = true
			break
		}
	}
	if !hasEntry {
		if err := session.AddAgent(p.cfg.Layout, sessionID, agentName, ""); err != nil {
			return err
		}
		sess, err = session.Load(p.cfg.Layout, sessionID)
		if err != nil {
			return err
		}
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

	br, err := p.cfg.Factory.Build(FactoryOptions{
		SessionID:    sessionID,
		AgentName:    agentName,
		ProviderType: pType,
		ProviderName: pType, // default-name = type until per-instance pickers ship
		Workspace:    cwd,
		ResumeID:     resumeID,
		IdleTimeout:   p.cfg.IdleTimeout,
		KillAfterIdle: p.cfg.KillAfterIdle,
		PresetName:   sess.Meta.Preset,
	})
	if err != nil {
		return err
	}
	a, st, sto := br.Agent, br.State, br.Store
	// Wire OnExit so the pool reclaims the slot.
	key := sessionKey(sessionID, agentName)
	entry := &runEntry{
		agent:   a,
		state:   st,
		store:   sto,
		sessID:  sessionID,
		agentNm: agentName,
		cwd:     cwd,
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
	st.MarkSpawning()
	if err := a.Start(ctx); err != nil {
		p.releaseSlot(key)
		return err
	}
	// Spawn metadata (pid + first user message) is only knowable here:
	// pid arrives from a.Start, first message from the buffer drain.
	if br.OnStarted != nil {
		br.OnStarted(SpawnStartMeta{
			PID:              a.PID(),
			Binary:           a.Binary(),
			Argv:             a.Argv(),
			FirstUserMessage: combined,
		})
	}
	if p.cfg.OnLifecycle != nil {
		p.cfg.OnLifecycle(LifecycleEvent{
			SessionID: sessionID,
			AgentName: agentName,
			Lifecycle: "spawning",
			PID:       a.PID(),
			At:        time.Now().UTC(),
		})
	}
	if combined != "" {
		// User turns were already persisted to conversation.jsonl by
		// persistBufferedTurn on each Send; combined is just the CLI input.
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
	p.mu.Lock()
	if entry, ok := p.active[key]; ok && entry.state != nil {
		entry.state.MarkKilled()
	}
	p.mu.Unlock()
	_ = p.markStatus(sessionID, session.StatusIdle)
	p.releaseSlot(key)
	if p.cfg.OnLifecycle != nil {
		p.cfg.OnLifecycle(LifecycleEvent{
			SessionID: sessionID,
			AgentName: agentName,
			Lifecycle: "killed",
			At:        time.Now().UTC(),
		})
	}
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

// ensureSession creates the session directory + meta.json when the session
// does not yet exist. Called by Send for channels (e.g. Slack) that supply
// their own session ID (thread_ts) without going through the UI create flow.
// Concurrent calls for the same ID are safe — session.Create returns a
// benign "already exists" error that we suppress.

// EnsureSession is the public wrapper for ensureSession. Workflow's
// session_init executor calls this to materialize the registry entry +
// sidebar row up-front, before any agent node actually dispatches a
// message. Idempotent — a second call for the same sessionID is a
// no-op (or backfills workspace).
func (p *Pool) EnsureSession(ctx context.Context, sessionID, source, workspace string) error {
	return p.ensureSession(ctx, sessionID, source, workspace)
}

func (p *Pool) ensureSession(ctx context.Context, sessionID, source, workspace string) error {
	existing, err := session.Load(p.cfg.Layout, sessionID)
	if err == nil {
		// Session exists — backfill workspace if it was created before one was configured.
		if workspace != "" && existing.Meta.Workspace == "" {
			if swErr := session.SwitchWorkspace(ctx, p.cfg.Layout, sessionID, workspace); swErr != nil {
				log.Warn().Str("session", sessionID).Str("workspace", workspace).Err(swErr).Msg("pool: backfill workspace failed")
			} else if updated, ldErr := session.Load(p.cfg.Layout, sessionID); ldErr == nil {
				if p.cfg.OnSessionCreated != nil {
					p.cfg.OnSessionCreated(updated)
				}
			}
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sess, cerr := session.Create(ctx, p.cfg.Layout, session.CreateOptions{
		ID:        sessionID,
		Origin:    session.Origin(source),
		Workspace: workspace,
	})
	// Suppress "already exists" — a concurrent call may have won the race.
	if cerr != nil && !errors.Is(cerr, os.ErrExist) {
		if cerr.Error() != fmt.Sprintf("session %q already exists", sessionID) {
			return cerr
		}
		return nil // race: other caller created it, that's fine
	}
	if p.cfg.OnSessionCreated != nil {
		p.cfg.OnSessionCreated(sess)
	}
	return nil
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

// setLabelIfEmpty writes the first user message as a sidebar label into
// meta.Label. No-op when a label is already set.
func (p *Pool) setLabelIfEmpty(sessionID, text string) {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil || sess.Meta.Label != "" {
		return
	}
	r := []rune(text)
	if len(r) > 60 {
		r = r[:60]
	}
	sess.Meta.Label = string(r)
	_ = session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta)
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
	alreadyClosed := p.closed
	p.closed = true
	entries := make([]*runEntry, 0, len(p.active))
	for _, e := range p.active {
		entries = append(entries, e)
	}
	p.mu.Unlock()
	if !alreadyClosed && p.stopCh != nil {
		close(p.stopCh)
	}
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
		entry := ActiveEntry{
			SessionID: e.sessID,
			AgentName: e.agentNm,
			CWD:       e.cwd,
		}
		if e.state != nil {
			entry.Lifecycle = e.state.Lifecycle().String()
			entry.Substate = e.state.Current().String()
			entry.LastActive = e.state.LastActive()
		}
		if e.agent != nil {
			entry.PID = e.agent.PID()
		}
		out = append(out, entry)
	}
	return out
}

// IdleTimeout returns the configured idle timeout. UI consumers use
// it to render the auto-kill countdown alongside LastActive.
func (p *Pool) IdleTimeout() time.Duration { return p.cfg.IdleTimeout }

// ActiveEntry is the public snapshot view of one running agent.
// Lifecycle / Substate / PID / LastActive are populated when the
// pool can read them; older callers that only check SessionID + AgentName
// keep working.
type ActiveEntry struct {
	SessionID  string
	AgentName  string
	CWD        string // resolved workspace path, used by RouteByCWD
	PID        int
	Lifecycle  string
	Substate   string
	LastActive time.Time
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

// Dequeue drops every queued request matching sessionID+agentName.
// Returns the number of removed entries — operators use this to
// cancel a session that has been waiting too long without ever
// getting a slot. Active spawns are NOT touched; use Kill for that.
func (p *Pool) Dequeue(sessionID, agentName string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.queue[:0]
	removed := 0
	for _, q := range p.queue {
		if q.sessionID == sessionID && q.agentName == agentName {
			removed++
			continue
		}
		out = append(out, q)
	}
	p.queue = out
	return removed
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
