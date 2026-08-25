package pool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/processctl"
)

// augmentWithAttachments returns text plus a trailing block listing
// the absolute on-disk paths of any uploaded files. The CLI subprocess
// reads the file content via its own Read/View tool — wick just hands
// it the paths. Returns text unchanged when atts is empty.
func augmentWithAttachments(text string, atts []store.Attachment) string {
	if len(atts) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("[Attached files]")
	for _, a := range atts {
		path := a.AbsPath
		if path == "" {
			path = a.StoredName
		}
		b.WriteString("\n- ")
		if a.Name != "" && a.Name != filepath.Base(path) {
			b.WriteString(a.Name)
			b.WriteString(": ")
		}
		b.WriteString(path)
	}
	return b.String()
}

// senderVisibility resolves the configured level, defaulting to
// store.SenderName when unset or unrecognised.
func (p *Pool) senderVisibility() string {
	if p.cfg.SenderVisibilityLoader == nil {
		return store.SenderName
	}
	return store.NormalizeSenderVisibility(p.cfg.SenderVisibilityLoader())
}

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
	// crashes tracks recent unexplained deaths per agent so a restart
	// budget can be enforced. Lazily created; see crashrecovery.go.
	crashes map[string]*crashState
	closed  bool
	stopCh  chan struct{} // closed by Stop to unwind background loops

	// wg tracks tryGrantQueue background spawns + onAgentExit work so
	// Stop can wait for all post-exit disk writes (markStatus, queue
	// drain) to finish before returning. Without this, tests that
	// observe Active==0 race the trailing meta.json writes.
	wg sync.WaitGroup

	// providerMax resolves a provider instance's MaxConcurrent cap. Defaults
	// to the registry lookup (provider.Find); tests inject a pure stub so
	// capacity math is exercised without touching disk or the global
	// provider registry (which Save mutates via an async rescan).
	providerMax func(pType, pName string) int
}

// PoolConfig knobs.
//
// A session with no project bound falls back to a per-session temp dir
// so claude still has a stable cwd. See agents-design.md §0.2 D4.
type PoolConfig struct {
	MaxConcurrent int
	IdleTimeout   time.Duration
	KillAfterIdle time.Duration
	// MinFreeMemoryLoader returns the floor, in MB, below which a spawn is
	// queued rather than started. 0 or a nil loader disables the check, so
	// a pool built without it behaves exactly as it always has.
	//
	// A loader rather than a value because the operator changes this in
	// the UI while wick is running — the same reason the factory's Gate
	// and MemGuard settings are loaders. MaxConcurrent above counts
	// PROCESSES; this counts BYTES, and a free slot says nothing about
	// whether the machine can host what would fill it.
	MinFreeMemoryLoader func() int
	// CallerUserID resolves the wick user behind a Send from its context.
	// The pool stays decoupled from the auth packages: the server injects
	// this. nil (or empty result) = no resolved caller, which disables
	// caller-change respawn for that message.
	CallerUserID func(ctx context.Context) string

	// SenderFrom resolves WHO sent a message from its context — the human
	// identity the originating channel read off its own transport envelope.
	// Injected for the same reason as CallerUserID: the pool must not import
	// the channels package.
	//
	// Distinct from CallerUserID, which answers "whose wick account does this
	// turn run as" (a permissions question, and the reason a process gets
	// recycled). This answers "who is the agent talking to" — a display and
	// prompting question. They diverge whenever a Slack user has no mapped
	// wick account, or when several people share one channel-owner identity.
	//
	// nil (or a nil result) = anonymous turn: no `[wick-sender ...]` prefix
	// and no sender persisted, exactly how every turn behaved before this
	// existed.
	SenderFrom func(ctx context.Context) *store.Sender

	// SenderVisibilityLoader reports how much of the sender to write into the
	// text handed to the model: SenderOff / SenderName / SenderNameID /
	// SenderFull. nil or an empty result means SenderName.
	//
	// A loader rather than a value because the operator changes this in the
	// UI while wick is running, same as the memory-guard settings. It governs
	// the model's copy only — the store and the dashboard always get the full
	// sender.
	SenderVisibilityLoader func() string

	// RevokeMCPToken invalidates a per-session MCP credential once its
	// subprocess is gone, so a leaked token stops working at process death
	// instead of at its TTL. nil = no revocation (tokens simply expire).
	RevokeMCPToken func(token string)

	// RespawnOnCallerChange makes a session whose incoming message comes
	// from a DIFFERENT user than the running subprocess kill and respawn
	// that subprocess, so the new user's turn runs under their own MCP
	// identity rather than inheriting the previous caller's.
	//
	// Off by default: it costs the process's context (conversation history
	// is reloaded, but in-memory state is lost), which is the wrong trade
	// for the common single-user session. Turn it on for shared sessions
	// where per-user attribution matters more than continuity.
	RespawnOnCallerChange bool

	// PreemptIdle, when true, lets a queued send kick out the longest-idle
	// active subprocess (Lifecycle == Idle) so the new session doesn't have
	// to wait for the idle TTL. The preempted session keeps its CLI session
	// ID in agents.json and resumes via --resume on its next message.
	PreemptIdle bool
	Layout      config.Layout
	Factory     AgentFactory
	// DefaultProvider is the instance key ("claude", "claude/work", ...)
	// used when a session's agent entry has no provider bound — the
	// channel/API/quick-create path where AddAgent is called with an empty
	// provider. Empty falls back to the per-type claude default (see
	// factory.go). Sourced from GeneralConfig.DefaultProvider.
	DefaultProvider string
	// OnSessionCreated is called after the pool auto-creates a session for a
	// channel message (e.g. Slack thread_ts). Wire this to
	// manager.Register so the dashboard sees the session immediately.
	OnSessionCreated func(s session.Session)
	// OnAgentAdded is called after the pool auto-adds an agent entry to
	// agents.json (channel sessions bypass the UI AddAgent flow). Wire
	// this to manager.RefreshSession so the in-memory registry reflects
	// the new agent before sendMessage resolves agentName.
	OnAgentAdded func(sessionID string)
	// OnSessionMeta is called after the pool mutates a session's meta on
	// disk (e.g. setLabelIfEmpty derives the first-message title). Wire
	// this to syncSessionMeta so the in-memory registry refreshes and the
	// new title broadcasts over SSE — otherwise the sidebar/list would
	// only catch up on the next page load. Optional; nil = no callback.
	OnSessionMeta func(sessionID string)
	// OnFirstUserMessage fires once per session, right after the first
	// user message has been persisted and the auto-label derived. Wired
	// to the ticket auto-create evaluator: its rules can key off the
	// message text, which is only knowable at this point — not when the
	// session was created. Runs in the caller's goroutine, so keep it
	// cheap; failures are the callback's own to log. Optional.
	OnFirstUserMessage func(sessionID, text string)
	// OnLifecycle fires when the pool transitions a session+agent's
	// lifecycle (Spawning, Killed). Idle/Working transitions are
	// implicit from event flow and are NOT routed here — UIs that
	// want every transition should subscribe to AgentEvent via
	// the factory's OnEvent. Optional; nil = no callback.
	OnLifecycle func(LifecycleEvent)
	// OnUserMessage fires when a user-role message is injected into a
	// session from a NON-web source (a channel or the schedule runner).
	// It lets the web SSE layer push the incoming turn to connected
	// viewers, which otherwise only see the assistant reply (the web
	// composer renders its own sends optimistically, so "ui"-sourced
	// turns are skipped here to avoid a duplicate). Optional; nil = no
	// callback.
	OnUserMessage func(UserMessageEvent)
	// OnSpawnError fires when a spawn fails before the agent process starts
	// (e.g. the CLI binary can't be launched). The wiring surfaces Message
	// as a live error event so it shows inline in the conversation instead of
	// only as a request-level error/toast; the pool has already persisted the
	// same text as a system error turn. Optional; nil = no callback.
	OnSpawnError func(SpawnErrorEvent)
}

// SpawnErrorEvent carries a failed-spawn notice to the SSE layer. Message is
// the human-readable text already persisted to conversation.jsonl; the raw
// fields are there for logging/formatting by the wiring.
type SpawnErrorEvent struct {
	SessionID    string
	AgentName    string
	Ctx          context.Context
	Message      string
	Err          error
	ProviderType string
	ProviderName string
}

// UserMessageEvent carries an injected user turn to the SSE layer so
// already-connected web viewers render it live instead of only after a
// manual refresh. Source is the origin ("slack", "telegram", "schedule",
// …) so the UI can badge where the message came from.
type UserMessageEvent struct {
	SessionID string
	AgentName string
	Source    string
	Text      string
	// Sender is who sent it, when the channel resolved one. Carried here so
	// a live-rendered turn shows the same sender chip a reloaded one does —
	// without it, a Slack message would arrive anonymous and only grow its
	// name after a refetch.
	Sender *store.Sender
}

// LifecycleEvent is emitted for the two transitions the pool drives
// directly (no parser event triggers them): a fresh spawn coming
// online, or a subprocess dying. PID is populated for spawning →
// working; 0 for killed.
type LifecycleEvent struct {
	SessionID    string
	AgentName    string
	Lifecycle    string // "spawning" | "killed"
	PID          int
	At           time.Time
	ProviderType string
	ProviderName string
	// Ctx is the spawn-time context from the originating Send. Carries
	// the zerolog logger the HTTP middleware attached, so callbacks
	// can `log.Ctx(ev.Ctx)` and recover the request_id. Never nil —
	// pool sets it to context.Background() when no spawn ctx applies
	// (e.g. exit fired from an already-released runEntry).
	Ctx context.Context
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
	// MCPToken is the per-session credential baked into this spawn's argv,
	// reported back so the pool can revoke it when the subprocess dies
	// rather than leaving it valid until its TTL. Empty when the spawn used
	// the shared internal token, which is per-boot and must NOT be revoked.
	MCPToken string
}

// SpawnStartMeta is the post-Start snapshot the pool feeds back to
// the factory so the spawn log gets a complete `start` record. PID,
// argv, and binary path are only knowable after Spawner.Spawn
// returns; FirstUserMessage comes from the buffer drain.
type SpawnStartMeta struct {
	PID              int
	Binary           string
	Argv             []string
	Env              []string
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
	// CallerUserID is the wick user whose message triggered this spawn.
	// Empty = no resolved caller (cron, system job, legacy session).
	CallerUserID  string
	SessionID     string
	AgentName     string
	ProviderType  string
	ProviderName  string
	Workspace     string
	ResumeID      string
	IdleTimeout   time.Duration
	KillAfterIdle time.Duration
	OnEvent       func(event.AgentEvent)
	// PresetName is the preset name from session meta. Factory resolves
	// the content from disk — pool passes the name so factory avoids a
	// redundant session.Load.
	PresetName string
	// SystemAddon is free-text prompt content appended AFTER the preset.
	// Carries per-session prompt text that has no named preset file — a
	// sub-agent role's system prompt, or a project's default addon.
	SystemAddon string
	// Origin is the session origin (e.g. "slack", "ui", "rest") written
	// into the spawn log so Recent Spawns can show the channel without a
	// registry lookup.
	Origin string
	// IsSubAgent selects the delegated-child immutable prompt instead of
	// the human-facing one. Set from the session's ParentSessionID — the
	// one property that makes a session a sub-agent — so a role or preset
	// cannot claim the wrong audience.
	IsSubAgent bool
	// Title / TitleCustom are the session's current title state, surfaced
	// in the "This session" system-prompt block so the agent knows
	// whether it still needs to set a title without a wick_session_info
	// round-trip. Snapshot at spawn time.
	Title       string
	TitleCustom bool
	// MaxTurns caps agentic turns on the spawn (--max-turns). Pulled from
	// the agent entry by the pool; 0 = no cap.
	MaxTurns int
	// ThinkingTokens is the resolved MAX_THINKING_TOKENS env value for the
	// spawn (claude). Pulled from the agent entry by the pool; empty = unset
	// (provider default, thinking on); "0" = disabled; "<n>" = budget.
	ThinkingTokens string
	// ModelID pins a model id on the spawn, scoped to the active provider
	// instance. Pulled from the agent entry by the pool; empty = that
	// provider's own default.
	ModelID string
}

// queueEntry is one request waiting for a slot.
type queueEntry struct {
	sessionID string
	agentName string
	enqueued  time.Time
}

// runEntry tracks an active agent in the pool.
type runEntry struct {
	agent   *provider.Agent
	state   *state.Machine
	store   *store.Store
	buffer  *Buffer
	sessID  string
	agentNm string
	cwd     string // resolved workspace path (used by RouteByCWD)
	// provType / provName identify the resolved provider for this entry,
	// used to enforce the per-provider concurrency cap alongside the
	// global one. Set at spawn time from the session's agents.json.
	provType string
	provName string
	// callerUserID is the wick user whose message caused this subprocess to
	// spawn. Its MCP credential is minted for that human, and the credential
	// is baked into the spawn argv — so it cannot change while the process
	// lives. When a DIFFERENT user sends into the same session, the process
	// must be replaced or the second user would act with the first user's
	// MCP identity. See Pool.callerChanged.
	//
	// Empty = spawned with no resolved caller (cron, system job, legacy
	// session); such a run is never recycled on caller grounds.
	callerUserID string
	// mcpToken is the per-session MCP credential handed to this spawn, kept
	// so it can be revoked the moment the subprocess dies instead of idling
	// until its TTL. Empty for spawns using the shared internal token, which
	// must never be revoked — it is per-boot and shared.
	mcpToken string
	// ctx is the spawn-time context (HTTP Send → pool.spawn). It
	// carries the zerolog logger the middleware attached, so async
	// post-spawn callbacks (onAgentExit, OnLifecycle) recover the
	// originating request_id via log.Ctx(ctx). The cancel signal
	// is intentionally NOT used to abort post-spawn work — only the
	// logger value is read.
	ctx context.Context

	// deadProbes counts consecutive ReconcileDead ticks where the entry
	// looked dead (idle + PID not alive). A zombie is only reaped after it
	// stays dead across reconcileDeadThreshold ticks, so a single transient
	// probe miss (e.g. OpenProcess momentarily failing) can never reap a
	// process that's actually alive. Reset to 0 the moment it probes alive.
	deadProbes int
}

// reconcileDeadThreshold is how many consecutive reconcile ticks an idle
// entry must probe dead before it's reaped. At the 3s tick cadence this is
// ~6s of sustained death — long enough to ride out a transient probe miss,
// short enough that a real zombie clears quickly.
const reconcileDeadThreshold = 2

// New returns an empty pool.
//
// MaxConcurrent <= 0 means UNLIMITED at the global scope (bounded only by
// per-provider caps and host resources). The config UI seeds a sane
// default of 2, but an operator may set 0 deliberately to lift the global
// ceiling — capacity math treats 0 as "no global cap".
func New(cfg PoolConfig) *Pool {
	if cfg.MaxConcurrent < 0 {
		cfg.MaxConcurrent = 0 // normalise negatives to the unlimited sentinel
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
		providerMax:  providerMaxConcurrent,
	}
	if cfg.PreemptIdle {
		p.wg.Add(1)
		go p.preemptLoop()
	}
	// Always run the dead-process reaper, even without PreemptIdle — a
	// crashed / externally-killed subprocess that the reader never saw
	// EOF leaves a zombie slot that must be reclaimed regardless.
	p.wg.Add(1)
	go p.reconcileLoop()
	return p
}

// reconcileLoop periodically reaps active entries whose subprocess died
// without the reader detecting it. 3s cadence keeps the UI badge close
// to realtime — signal-0 probes are cheap. When a dead entry is reaped,
// Stop → exit hook → OnLifecycle broadcasts pool_stats, so the Process
// badge drops on its own without needing a manual refresh.
func (p *Pool) reconcileLoop() {
	defer p.wg.Done()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.mu.Lock()
			closed := p.closed
			empty := len(p.active) == 0
			p.mu.Unlock()
			if closed || empty {
				continue
			}
			p.ReconcileDead()
		}
	}
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

// AutoReplyOn reports the persisted Slack auto-reply flag (meta.json). It
// reads the session meta fresh so a restart picks up the last saved state.
// Missing session or read error → false (fail closed).
func (p *Pool) AutoReplyOn(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return false
	}
	return sess.Meta.AutoReply
}

// SetAutoReply persists the Slack auto-reply flag on the session meta. A
// missing session or save error is logged and swallowed — the in-memory
// fallback in the channel keeps the turn working even if persistence fails.
func (p *Pool) SetAutoReply(sessionID string, on bool) {
	if sessionID == "" {
		return
	}
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		log.Warn().Str("session", sessionID).Bool("on", on).Err(err).Msg("pool: set auto-reply — session load failed")
		return
	}
	if sess.Meta.AutoReply == on {
		return
	}
	sess.Meta.AutoReply = on
	if err := session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta); err != nil {
		log.Warn().Str("session", sessionID).Bool("on", on).Err(err).Msg("pool: set auto-reply — save failed")
	}
}

func (p *Pool) Send(ctx context.Context, sessionID, agentName, source, role, text string) error {
	return p.send(ctx, sessionID, agentName, source, role, text, "", nil)
}

// SendWithProject is like Send but binds sessionID to the given project id
// when auto-creating the session. Pass an empty string for the default.
func (p *Pool) SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error {
	return p.send(ctx, sessionID, agentName, source, role, text, projectID, nil)
}

// SendWithAttachments is Send with a list of user-uploaded files. The
// caller is responsible for materializing the files on disk under
// SessionDir/uploads/ — the pool only persists the metadata into
// conversation.jsonl and appends a small `[Attached files]` block to
// the text sent to the CLI subprocess so it can Read the paths.
func (p *Pool) SendWithAttachments(ctx context.Context, sessionID, agentName, source, role, text, projectID string, atts []store.Attachment) error {
	return p.send(ctx, sessionID, agentName, source, role, text, projectID, atts)
}

// resolveAgentName turns an empty agent name into the session's actual one:
// Meta.ActiveAgent, else the first entry on disk.
//
// Same rule switchProvider and session.ActiveRunTarget already use, so
// "no name given" resolves identically no matter which door the message came
// through — a sub-agent result, a channel, or a person in the composer.
//
// A name is returned unchanged, and a session with no entries keeps the empty
// name: the caller's spawn path creates the entry from its own defaults, and
// inventing a name here would pre-empt that with one nothing else knows.
func (p *Pool) resolveAgentName(sessionID, agentName string) string {
	if agentName != "" {
		return agentName
	}
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil || len(sess.Agents) == 0 {
		return agentName
	}
	if active := sess.Meta.ActiveAgent; active != "" {
		for _, a := range sess.Agents {
			if a.Name == active {
				return active
			}
		}
	}
	return sess.Agents[0].Name
}

// callerChanged reports whether this message comes from a different user
// than the one the running subprocess was spawned for.
//
// Returns false whenever either side is unknown: a spawn with no recorded
// caller (cron, system job, legacy session) has no identity to conflict with,
// and a message with no resolved caller gives nothing to compare — recycling
// on a blank would kill a healthy process for no benefit.
func (p *Pool) callerChanged(ctx context.Context, entry *runEntry) bool {
	if entry == nil || entry.callerUserID == "" || p.cfg.CallerUserID == nil {
		return false
	}
	now := p.cfg.CallerUserID(ctx)
	return now != "" && now != entry.callerUserID
}

func (p *Pool) send(ctx context.Context, sessionID, agentName, source, role, text, projectID string, atts []store.Attachment) error {
	// An empty agent name means "whoever this session is talking to" — the
	// same thing a person typing in the composer means. Resolved to the
	// session's real agent before anything keys off it.
	//
	// Without this, a sub-agent's result (delivered with no agent name,
	// because nothing populates ParentAgent) keyed on sessionID+"" — which
	// matches no live agent, so it never reached the leader's running
	// process. It spawned a second, nameless track instead and every
	// name-keyed write after it landed on a row nobody reads.
	agentName = p.resolveAgentName(sessionID, agentName)

	// Who is speaking, per the originating channel. Resolved once here so
	// the live and buffered paths below agree, and only for "user" turns:
	// a system context block or a sub-agent result has no human behind it,
	// and stamping one would put a person's name on a message they never
	// sent.
	var sender *store.Sender
	if role == "user" && p.cfg.SenderFrom != nil {
		sender = p.cfg.SenderFrom(ctx)
	}
	// Read once per send so the live and buffered paths below agree even if
	// the operator changes the setting mid-flight.
	senderLevel := p.senderVisibility()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("pool closed")
	}
	key := sessionKey(sessionID, agentName)
	entry, alive := p.active[key]
	p.mu.Unlock()

	// A live subprocess speaks with the MCP identity of whoever spawned it —
	// the credential sits in its argv and cannot be swapped in place. So when
	// a DIFFERENT user sends into the same session, reusing that process would
	// run their turn under the previous caller's identity. Recycle instead:
	// kill it here and let the spawn below mint a credential for this caller.
	//
	// Only when explicitly enabled, and only for role "user": a system or
	// sub-agent message is not a human taking over the conversation.
	if alive && p.cfg.RespawnOnCallerChange && role == "user" && p.callerChanged(ctx, entry) {
		log.Ctx(ctx).Info().
			Str("component", "pool").
			Str("session", sessionID).
			Str("agent", agentName).
			Msg("pool.send: caller changed — respawning so the turn runs as the new user")
		// Kill synchronously so the slot is free before the spawn below;
		// onAgentExit revokes the outgoing caller's MCP token.
		//
		// If the kill fails the old process is still attached, and sending
		// into it would run this user's turn under the previous caller's MCP
		// identity — exactly what the recycle exists to prevent. Refuse the
		// message instead of silently misattributing it.
		if err := p.Kill(sessionID, agentName); err != nil {
			log.Ctx(ctx).Error().Err(err).
				Str("component", "pool").
				Str("session", sessionID).
				Str("agent", agentName).
				Msg("pool.send: caller-change respawn failed to stop the old process")
			return fmt.Errorf("session is running as another user and could not be recycled: %w", err)
		}
		alive = false
		entry = nil
	}

	turnPersisted := false
	userMsgNotified := false
	if alive {
		log.Ctx(ctx).Debug().
			Str("component", "pool").
			Str("session", sessionID).
			Str("agent", agentName).
			Str("role", role).
			Str("source", source).
			Int("text_len", len(text)).
			Int("attachments", len(atts)).
			Msg("pool.send: routing to live subprocess")
		// Active agent — append to conversation log + send straight.
		if entry.store != nil {
			_ = entry.store.AppendUserTurnWithSender(role, source, text, atts, sender)
			turnPersisted = true
		}
		if role == "user" {
			p.setLabelIfEmpty(sessionID, text)
			p.notifyUserMessage(sessionID, agentName, source, text, sender)
			userMsgNotified = true
		}
		err := entry.agent.Send(store.PrependSenderLine(augmentWithAttachments(text, atts), sender, senderLevel))
		// Nudge SSE so the Process panel's queued count updates in
		// realtime — a RespawnQueue (codex) Send while busy just appended
		// to the agent's pending queue, which fires no lifecycle event on
		// its own.
		p.notifyLifecycle(ctx, entry, sessionID, agentName)
		if err != nil && err.Error() == "agent not running" {
			// Race: subprocess exited between the active-map lookup and
			// Send — onAgentExit hasn't released the slot yet. Evict the
			// stale entry and fall through to the buffer+spawn path so
			// the message isn't lost.
			log.Ctx(ctx).Warn().
				Str("component", "pool").
				Str("session", sessionID).
				Str("agent", agentName).
				Msg("pool.send: stale active entry (race), evicting and respawning")
			p.releaseSlot(key)
			alive = false
		} else {
			return err
		}
	}
	log.Ctx(ctx).Debug().
		Str("component", "pool").
		Str("session", sessionID).
		Str("agent", agentName).
		Str("role", role).
		Str("source", source).
		Int("text_len", len(text)).
		Msg("pool.send: no live subprocess — buffer + spawn-or-queue")

	// Not active. Ensure the session exists on disk (channels like Slack
	// pass a thread_ts as the session ID; the session is never created
	// via the UI flow).
	if err := p.ensureSession(ctx, sessionID, source, projectID); err != nil {
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
	// Buffer the agent-facing text (sender line prepended, attachment block
	// appended) so the first drain after spawn carries both the identity of
	// the sender and any file references. Storage gets the un-augmented text
	// plus the structured atts/sender.
	//
	// The sender line has to go into the buffer rather than being added at
	// drain time: a buffer can accumulate messages from several people
	// before the subprocess exists, and drain concatenates them into one
	// prompt. Stamping at drain would label every one of them with whoever
	// happened to send last.
	if err := buf.Append(store.PrependSenderLine(augmentWithAttachments(text, atts), sender, senderLevel)); err != nil {
		return err
	}
	// Persist the user turn to conversation.jsonl immediately so a page
	// refresh while the session is buffered (queued or mid-spawn) still
	// shows the messages — they previously only lived in PendingInput.
	// We build a transient Store because no entry.store exists yet.
	if !turnPersisted {
		p.persistBufferedTurn(sessionID, agentName, role, source, text, atts, sender)
	}
	if role == "user" && !userMsgNotified {
		p.notifyUserMessage(sessionID, agentName, source, text, sender)
	}

	// A non-user turn (e.g. the one-time origin-context block channels
	// inject before the first user message) must NOT spawn the agent on
	// its own. It is now buffered; the user turn that follows will spawn
	// and Drain picks up both as one combined prompt. Spawning here would
	// run the agent against just the context — it would reply "I don't
	// see a request" and then reply again when the real message lands
	// (two replies for one prompt). Channels always send the user turn
	// right after, so the buffered context is never stranded.
	if role != "user" {
		return nil
	}

	// Resolve the provider for this session so the slot check can enforce
	// the per-provider cap alongside the global one.
	pType, pName := p.providerForSession(sessionID, agentName)

	p.mu.Lock()
	// If this session is already mid-spawn, the in-flight spawn's Drain
	// will pick up the buffered message — nothing more to do here.
	if _, spawning := p.spawningKeys[key]; spawning {
		p.mu.Unlock()
		return nil
	}
	// Spawn only when both the global and per-provider caps allow it;
	// otherwise queue. spawningKeys counted in so concurrent Sends can't
	// each see a free slot.
	if p.slotFreeLocked(pType, pName) {
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
func (p *Pool) persistBufferedTurn(sessionID, agentName, role, source, text string, atts []store.Attachment, sender *store.Sender) {
	sto := store.New(store.Options{
		Layout:    p.cfg.Layout,
		SessionID: sessionID,
		AgentName: agentName,
	})
	_ = sto.AppendUserTurnWithSender(role, source, text, atts, sender)
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
	// Register the Stop goroutine while still holding the lock that guards
	// p.closed. Adding after the unlock leaves a window for Stop() to close
	// the pool and reach wg.Wait() in between, and sync.WaitGroup forbids an
	// Add racing a Wait already blocked at zero.
	if victim != nil {
		p.wg.Add(1)
	}
	p.mu.Unlock()
	if victim == nil {
		return false
	}
	log.Debug().Str("session", victim.sessID).Str("agent", victim.agentNm).
		Msg("pool: preempting idle slot for queued session")
	go func() {
		defer p.wg.Done()
		_ = victim.agent.Stop()
	}()
	return true
}

// spawn allocates a slot, builds an agent via the factory, drains the
// buffer into one combined input, and starts it. Caller must NOT hold
// p.mu (we acquire and release it ourselves so the spawn can take
// time without blocking other Send calls).
// composeSystemAddon assembles the free-text prompt appended after the
// named preset: the session's project default first, then the session's
// own addon.
//
// That order is deliberate — the project addon is shared context ("this
// repo uses X"), while the session addon is the specific instruction (a
// sub-agent role's prompt). The specific one comes last so it can refine
// the general one rather than being buried by it.
//
// Resolved per spawn rather than copied at session creation, so editing a
// project's default reaches existing sessions on their next respawn.
func composeSystemAddon(layout config.Layout, sess session.Session) string {
	parts := make([]string, 0, 2)
	if sess.Meta.ProjectID != "" {
		if proj, err := project.Load(layout, sess.Meta.ProjectID); err == nil {
			if v := strings.TrimSpace(proj.Meta.Defaults.SystemAddon); v != "" {
				parts = append(parts, v)
			}
		}
	}
	if v := strings.TrimSpace(sess.Meta.SystemAddon); v != "" {
		parts = append(parts, v)
	}
	return strings.Join(parts, "\n\n")
}

func (p *Pool) spawn(ctx context.Context, sessionID, agentName, source string) error {
	// Whose turn caused this process to exist. Baked into the MCP credential
	// below, and remembered on the entry so a later message from someone else
	// can be told apart.
	var callerUserID string
	if p.cfg.CallerUserID != nil {
		callerUserID = p.cfg.CallerUserID(ctx)
	}
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
	// An entry that exists but names NO provider needs the same treatment as
	// a missing one. Without this, a blank provider fell straight through to
	// factory.go's per-type default, silently landing the spawn on claude no
	// matter what the project asked for — and there was no way to tell that
	// from a deliberate choice.
	hasEntry := false
	blankProvider := false
	for _, a := range sess.Agents {
		if a.Name == agentName {
			hasEntry = true
			blankProvider = strings.TrimSpace(a.Provider) == ""
			break
		}
	}
	if !hasEntry || blankProvider {
		// Provider precedence: the session's project default → the global
		// default → (empty →) the per-type claude default in factory.go.
		// Channels create sessions without going through the UI's AddAgent,
		// so this is the only place a channel spawn picks up the project's
		// configured provider.
		//
		// Deliberately NOT parent-aware. A sub-agent's provider is resolved by
		// the delegation service before its entry is written (see
		// delegation.resolveChildTarget), so a child never arrives here with a
		// blank one; adding a second, subtly different chain here would be two
		// places to keep in step for a case that cannot happen.
		//
		// The project's model travels WITH its provider — a pin belongs to
		// the instance it was chosen on, so taking one without the other
		// resolves against the wrong model registry. The global default
		// contributes no model (it is a bare instance key).
		prov := p.cfg.DefaultProvider
		provModel := ""
		if sess.Meta.ProjectID != "" {
			if proj, perr := project.Load(p.cfg.Layout, sess.Meta.ProjectID); perr == nil && proj.Meta.Defaults.Provider != "" {
				prov = proj.Meta.Defaults.Provider
				provModel = proj.Meta.Defaults.Model
			}
		}
		if hasEntry {
			// Repair in place: AddAgent refuses a duplicate name, and the
			// entry carries a resume id we must not discard.
			if err := session.SetAgentProvider(p.cfg.Layout, sessionID, agentName, prov); err != nil {
				return err
			}
		} else if err := session.AddAgent(p.cfg.Layout, sessionID, agentName, prov); err != nil {
			return err
		}
		// Only write the inherited pin when the entry has none of its own:
		// a session already pinned to a model keeps it, and an empty
		// provModel must not blank an existing pin.
		if provModel != "" {
			if err := session.SetModelIDIfEmpty(p.cfg.Layout, sessionID, agentName, provModel); err != nil {
				return err
			}
		}
		sess, err = session.Load(p.cfg.Layout, sessionID)
		if err != nil {
			return err
		}
		if !hasEntry && p.cfg.OnAgentAdded != nil {
			p.cfg.OnAgentAdded(sessionID)
		}
	}
	resumeID := ""
	maxTurns := 0
	thinkingTokens := ""
	modelID := ""
	pType := ""
	pName := ""
	for _, a := range sess.Agents {
		if a.Name == agentName {
			resumeID = a.CLISessionID
			maxTurns = a.MaxTurns
			thinkingTokens = a.ThinkingTokens
			modelID = a.ModelID
			// A pin only means anything on the instance it was chosen on.
			// wick's ids are opaque ("m_…", or "<entry>@<vendorModel>") and a
			// CLI given one dies on `--model m_…`. This happens when a pin
			// outlives its instance — a session switched away from wick, or a
			// child that inherited its parent's wick pin before being
			// repointed. Dropped rather than carried, so the target resolves
			// its own default instead of failing to spawn.
			if modelID != "" && !strings.HasPrefix(a.Provider, "wick") &&
				(strings.ContainsRune(modelID, '@') || strings.HasPrefix(modelID, "m_")) {
				log.Warn().
					Str("session", sessionID).
					Str("provider", a.Provider).
					Str("model", modelID).
					Msg("agents.spawn: dropping a wick model pin that does not belong to this provider")
				modelID = ""
			}
			// Provider field is stored as "type/name" (e.g. "claude/work").
			// Fall back to bare type when no slash present.
			if idx := strings.Index(a.Provider, "/"); idx >= 0 {
				pType = a.Provider[:idx]
				pName = a.Provider[idx+1:]
			} else {
				pType = a.Provider
				pName = a.Provider
			}
			break
		}
	}

	cwd, err := p.resolveCwd(sess)
	if err != nil {
		return err
	}

	br, err := p.cfg.Factory.Build(FactoryOptions{
		CallerUserID:   callerUserID,
		SessionID:      sessionID,
		AgentName:      agentName,
		ProviderType:   pType,
		ProviderName:   pName,
		Workspace:      cwd,
		ResumeID:       resumeID,
		IdleTimeout:    p.cfg.IdleTimeout,
		KillAfterIdle:  p.cfg.KillAfterIdle,
		PresetName:     sess.Meta.Preset,
		SystemAddon:    composeSystemAddon(p.cfg.Layout, sess),
		Origin:         string(sess.Meta.Origin),
		IsSubAgent:     sess.Meta.ParentSessionID != "",
		Title:          sess.Meta.Label,
		TitleCustom:    sess.Meta.TitleCustom,
		MaxTurns:       maxTurns,
		ThinkingTokens: thinkingTokens,
		ModelID:        modelID,
	})
	if err != nil {
		return err
	}
	a, st, sto := br.Agent, br.State, br.Store
	// Wire OnExit so the pool reclaims the slot.
	key := sessionKey(sessionID, agentName)
	entry := &runEntry{
		agent:    a,
		state:    st,
		store:    sto,
		sessID:   sessionID,
		agentNm:  agentName,
		cwd:      cwd,
		ctx:      ctx,
		provType: pType,
		provName: pName,
		// Remember who this process speaks for, and with which credential,
		// so a later message from a DIFFERENT user can be detected (the MCP
		// identity is fixed in the argv) and so the credential dies with the
		// process instead of idling until its TTL.
		callerUserID: callerUserID,
		mcpToken:     br.MCPToken,
	}
	// Spawn-local logger derived from the same ctx — consumers in the
	// rest of this function reuse it without redoing the With() chain.
	l := log.Ctx(ctx).With().
		Str("component", "pool").
		Str("session", sessionID).
		Str("agent", agentName).
		Logger()
	// Wire the state machine's lifecycle hook into the pool's
	// OnLifecycle callback so working↔idle transitions reach SSE the
	// same path as spawning/killed. BE becomes the source of truth;
	// the FE just listens to "lifecycle" events.
	if p.cfg.OnLifecycle != nil {
		st.SetLifecycleHook(func(from, to state.Lifecycle) {
			p.cfg.OnLifecycle(LifecycleEvent{
				SessionID:    sessionID,
				AgentName:    agentName,
				Lifecycle:    to.String(),
				Ctx:          ctx,
				PID:          a.PID(),
				At:           time.Now().UTC(),
				ProviderType: pType,
				ProviderName: pName,
			})
		})
	}
	// Every spawn drains the session's buffer, so the buffer must exist for
	// every spawn — not only for sessions that entered through send().
	//
	// A bare p.buffers lookup here left entry.buffer nil for any session
	// that reached the queue WITHOUT a send: a leader woken by a
	// delegation result, a preempt-requeue. The nil then panicked in
	// Drain, and because queue grants spawn on a bare goroutine
	// (tryGrantQueue), that single nil took the whole process down.
	// Observed in the wild. bufferFor lazy-creates and, crucially, reads
	// any PendingInput persisted on disk — exactly what a wake-path spawn
	// should drain.
	buf, err := p.bufferFor(sessionID)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if p.closed {
		// Stop raced ahead of us — bail before publishing the entry,
		// otherwise its later idle exit would call wg.Add after Stop's
		// wg.Wait has returned.
		p.mu.Unlock()
		return errors.New("pool closed")
	}
	entry.buffer = buf
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
	l.Debug().
		Int("first_input_len", len(combined)).
		Msg("pool.spawn: starting subprocess")
	if err := a.Start(ctx); err != nil {
		l.Error().
			Err(err).
			Msg("pool.spawn: Start failed")
		// A failed start leaves the state machine at Spawning and the session
		// marked Running. Tear down the same way a normal exit does so the UI
		// doesn't sit stuck on "spawning": flip to Killed (broadcasts the
		// lifecycle), revert status, release the slot, and let the queue move
		// on. Surface the failure as a system error turn (persisted + live) so
		// it reads inline instead of only as a request-level error.
		label := pType
		if pName != "" && pName != pType {
			label = pName + " " + pType
		}
		if label == "" {
			label = agentName
		}
		msg := "Failed to start " + label + ": " + err.Error()
		if sto != nil {
			if perr := sto.AppendErrorTurn(msg); perr != nil {
				l.Warn().Err(perr).Msg("pool.spawn: persist spawn-error turn failed")
			}
		}
		p.mu.Lock()
		if st != nil {
			st.MarkKilled()
		}
		p.mu.Unlock()
		_ = p.markStatus(sessionID, session.StatusIdle)
		p.releaseSlot(key)
		if p.cfg.OnSpawnError != nil {
			p.cfg.OnSpawnError(SpawnErrorEvent{
				SessionID:    sessionID,
				AgentName:    agentName,
				Ctx:          ctx,
				Message:      msg,
				Err:          err,
				ProviderType: pType,
				ProviderName: pName,
			})
		}
		p.tryGrantQueue()
		// Return nil: the failure is now surfaced in-band (persisted system
		// error turn + broadcast Error/Done, and channels get it via the event
		// dispatch) exactly like a runtime error. Bubbling it as a Send error
		// too would make /send return 500 and pop a toast — a double report the
		// caller should not see. The Send "succeeds"; the session is already
		// back to idle.
		return nil
	}
	l.Debug().
		Int("pid", a.PID()).
		Msg("pool.spawn: subprocess started")
	// Persist active agent so /api/sessions/{id}/meta returns it even
	// after the subprocess exits (idle session still shows provider label).
	label := pType
	if pName != "" && pName != pType {
		label = pName + " " + pType
	}
	if label == "" {
		label = agentName
	}
	_ = session.SetActiveAgent(p.cfg.Layout, sessionID, label)
	// Spawn metadata (pid + first user message) is only knowable here:
	// pid arrives from a.Start, first message from the buffer drain.
	if br.OnStarted != nil {
		br.OnStarted(SpawnStartMeta{
			PID:              a.PID(),
			Binary:           a.Binary(),
			Argv:             a.Argv(),
			Env:              a.Env(),
			FirstUserMessage: combined,
		})
	}
	if p.cfg.OnLifecycle != nil {
		p.cfg.OnLifecycle(LifecycleEvent{
			SessionID:    sessionID,
			AgentName:    agentName,
			Lifecycle:    "spawning",
			Ctx:          ctx,
			PID:          a.PID(),
			At:           time.Now().UTC(),
			ProviderType: pType,
			ProviderName: pName,
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
	key := sessionKey(sessionID, agentName)
	p.mu.Lock()
	// Join p.wg only while the pool is open. An exit can fire at any moment,
	// including after Stop() has begun waiting, and sync.WaitGroup forbids an
	// Add that races a Wait already blocked at zero. Reading p.closed under
	// the same lock Stop() sets it under makes the two mutually exclusive:
	// either we register before Stop() looks, or we see it closed and stay
	// out. Staying out loses nothing — the body below still runs to release
	// the slot, and Stop() waits for that through its p.active poll rather
	// than through wg.
	if !p.closed {
		p.wg.Add(1)
		defer p.wg.Done()
	}
	entry, ok := p.active[key]
	if ok && entry.state != nil {
		// MarkKilled flips the state machine → its lifecycle hook
		// broadcasts the killed transition to SSE. Pool no longer
		// emits a duplicate OnLifecycle("killed") below to avoid the
		// FE receiving two killed events per exit.
		entry.state.MarkKilled()
	}
	p.mu.Unlock()
	// Recover the spawn-time ctx (carries request_id from the
	// originating HTTP middleware). Fall back to Background when the
	// entry was already released by a racing caller — rare and harmless,
	// the log line just won't have request_id.
	ctx := context.Background()
	if ok {
		ctx = entry.ctx
	}
	l := log.Ctx(ctx).With().
		Str("component", "pool").
		Str("session", sessionID).
		Str("agent", agentName).
		Logger()
	l.Debug().Msg("pool.exit: subprocess exited — releasing slot")
	// Revoke the dead spawn's per-session MCP credential now rather than
	// letting it stay valid until its TTL: the process that held it is gone,
	// so nothing legitimate still needs it. Only per-session tokens are set
	// here — the shared per-boot token is never reported to the entry, so it
	// can't be revoked out from under other spawns.
	if ok && entry != nil && entry.mcpToken != "" && p.cfg.RevokeMCPToken != nil {
		p.cfg.RevokeMCPToken(entry.mcpToken)
		l.Debug().Msg("pool.exit: revoked per-session MCP token")
	}
	_ = p.markStatus(sessionID, session.StatusIdle)
	p.releaseSlot(key)
	p.tryGrantQueue()
}

// notifyLifecycle re-fires the OnLifecycle hook with the entry's current
// lifecycle. Used to nudge SSE (pool_stats rebuild) when something the
// state machine doesn't emit changes — e.g. a message queued onto a busy
// RespawnQueue agent. No-op when no hook or no state.
func (p *Pool) notifyLifecycle(ctx context.Context, entry *runEntry, sessionID, agentName string) {
	if p.cfg.OnLifecycle == nil || entry == nil || entry.state == nil {
		return
	}
	p.cfg.OnLifecycle(LifecycleEvent{
		SessionID:    sessionID,
		AgentName:    agentName,
		Lifecycle:    entry.state.Lifecycle().String(),
		Ctx:          ctx,
		PID:          entry.agent.PID(),
		At:           time.Now().UTC(),
		ProviderType: entry.provType,
		ProviderName: entry.provName,
	})
}

// notifyUserMessage fires the OnUserMessage hook so the SSE layer can push
// an injected user turn to connected web viewers. Skips web-sourced turns
// ("ui"): the composer already renders those optimistically, so echoing one
// back would double it. No-op when no hook or empty text.
func (p *Pool) notifyUserMessage(sessionID, agentName, source, text string, sender *store.Sender) {
	if p.cfg.OnUserMessage == nil {
		return
	}
	if source == "ui" || strings.TrimSpace(text) == "" {
		return
	}
	p.cfg.OnUserMessage(UserMessageEvent{
		SessionID: sessionID,
		AgentName: agentName,
		Source:    source,
		Text:      text,
		Sender:    sender,
	})
}

func (p *Pool) releaseSlot(key string) {
	p.mu.Lock()
	if e, ok := p.active[key]; ok {
		delete(p.buffers, e.sessID)
	}
	delete(p.active, key)
	p.mu.Unlock()
}

// slotFreeLocked reports whether a new spawn for the given provider may
// proceed under both the global cap and that provider's per-instance cap.
// Caller MUST hold p.mu. spawningKeys count in so concurrent Sends can't
// each see a free slot. pType/pName "" skips the per-provider check
// (callers that don't know the provider yet — rare).
//
// Per-provider cap comes from provider.Find(type,name).MaxConcurrent;
// 0 = unlimited (the instance follows only the global cap).
func (p *Pool) slotFreeLocked(pType, pName string) bool {
	// A free slot says nothing about whether the machine can host what
	// would fill it: the caps count processes, this counts bytes. Checked
	// here rather than at the two call sites so a memory refusal queues
	// through exactly the same path as a slot refusal.
	if !p.memoryAdmitsNow() {
		return false
	}
	// Remaining != 0 means free: a positive count, or -1 for unlimited.
	if pType == "" {
		// Provider unknown — gate on the global cap only.
		return p.capacityLocked().Remaining != 0
	}
	return p.providerCapacityLocked(pType, pName).Remaining != 0
}

// providerForSession reads the session's agents.json and returns the
// resolved provider type/name for the agent. Empty strings when the
// session or agent can't be loaded — callers treat that as "global cap
// only". Mirrors the parse in spawn().
func (p *Pool) providerForSession(sessionID, agentName string) (pType, pName string) {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return "", ""
	}
	for _, a := range sess.Agents {
		if a.Name != agentName {
			continue
		}
		if idx := strings.Index(a.Provider, "/"); idx >= 0 {
			return a.Provider[:idx], a.Provider[idx+1:]
		}
		return a.Provider, a.Provider
	}
	return "", ""
}

// providerMaxConcurrent looks up the per-instance MaxConcurrent for a
// provider. Returns 0 (unlimited) when the instance can't be resolved.
func providerMaxConcurrent(pType, pName string) int {
	ins, err := provider.Find(provider.Type(pType), pName)
	if err != nil {
		return 0
	}
	return ins.MaxConcurrent
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
	// Find the first queued entry whose provider still has a free slot
	// (global + per-provider). A head-of-line entry blocked by its
	// provider cap shouldn't starve a different provider behind it.
	idx := -1
	for i, q := range p.queue {
		pType, pName := p.providerForSession(q.sessionID, q.agentName)
		if p.slotFreeLocked(pType, pName) {
			idx = i
			break
		}
	}
	if idx < 0 {
		p.mu.Unlock()
		return
	}
	q := p.queue[idx]
	p.queue = append(p.queue[:idx], p.queue[idx+1:]...)
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

// SetMaxTurns persists the per-spawn turn cap on the session's agent
// entry (creating it if missing) so the next spawn passes --max-turns.
func (p *Pool) SetMaxTurns(sessionID, agentName string, maxTurns int) error {
	return session.SetMaxTurns(p.cfg.Layout, sessionID, agentName, maxTurns)
}

// SetThinkingTokens persists the resolved MAX_THINKING_TOKENS env value on
// the session's agent entry (creating it if missing) so the next spawn
// applies it. Empty = unset (provider default); "0" = disabled; "<n>" =
// token budget.
func (p *Pool) SetThinkingTokens(sessionID, agentName, v string) error {
	return session.SetThinkingTokens(p.cfg.Layout, sessionID, agentName, v)
}

// EnsureSession is the public wrapper for ensureSession. Workflow's
// session_init executor calls this to materialize the registry entry +
// sidebar row up-front, before any agent node actually dispatches a
// message. Idempotent — a second call for the same sessionID is a
// no-op (or backfills project binding).
func (p *Pool) EnsureSession(ctx context.Context, sessionID, source, projectID string) error {
	return p.ensureSession(ctx, sessionID, source, projectID)
}

// EnsureSessionOwner stamps UserID on an existing session when the session
// currently has no owner. No-op when the session does not exist or already
// has an owner.
//
// The OnSessionMeta callback at the end is load-bearing, not cosmetic. This
// writes to DISK, but the per-spawn MCP credential is minted from the IN-MEMORY
// registry (see SessionMCPToken). Without the refresh the registry keeps
// serving a session with no owner, so every later spawn falls back to the
// shared internal token and the agent runs as the synthetic admin — even though
// the owner was recorded correctly. That was the "wick_me says
// wick-agent-internal" bug, and it survived retries precisely because the disk
// was right and only the cache was wrong.
func (p *Pool) EnsureSessionOwner(ctx context.Context, sessionID, userID string) {
	if sessionID == "" || userID == "" {
		return
	}
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil || sess.Meta.UserID != "" {
		return
	}
	sess.Meta.UserID = userID
	if err := session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta); err != nil {
		log.Warn().Err(err).
			Str("session", sessionID).
			Str("user_id", userID).
			Msg("pool: failed to persist session owner")
		return
	}
	if p.cfg.OnSessionMeta != nil {
		p.cfg.OnSessionMeta(sessionID)
	}
}

func (p *Pool) ensureSession(ctx context.Context, sessionID, source, projectID string) error {
	existing, err := session.Load(p.cfg.Layout, sessionID)
	if err == nil {
		// Backfill project only before a conversation exists; moving the
		// cwd after would orphan the resumable session (claude is per-cwd).
		if projectID != "" && existing.Meta.ProjectID == "" && !sessionHasCLISession(existing) {
			if swErr := session.SetProject(ctx, p.cfg.Layout, sessionID, projectID); swErr != nil {
				log.Warn().Str("session", sessionID).Str("project", projectID).Err(swErr).Msg("pool: backfill project failed")
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
		ProjectID: projectID,
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
//  1. session.Meta.ProjectID — explicit binding
//  2. <BaseDir>/sessions/<id>/cwd/ — per-session temp dir
//
// A project is never mandatory at session create; unbound sessions get
// a stable per-session cwd. For a bound project the returned path is the
// project's resolved cwd (managed `files/` or custom path). The pool
// MkdirAll's managed paths so the spawn never fails on a missing
// directory; custom paths are assumed to exist (validated at project
// create time).
func (p *Pool) resolveCwd(sess session.Session) (string, error) {
	id := sess.Meta.ProjectID
	if id != "" && project.Exists(p.cfg.Layout, id) {
		path, err := project.ResolvePath(p.cfg.Layout, id)
		if err != nil {
			return "", fmt.Errorf("resolve project %q: %w", id, err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("ensure project path %q: %w", path, err)
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
// meta.Label. No-op when a label is already set or when the title was
// explicitly chosen by a human / the agent (TitleCustom) — a custom
// title must never be clobbered by the auto-derived first message.
func (p *Pool) setLabelIfEmpty(sessionID, text string) {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil || sess.Meta.Label != "" || sess.Meta.TitleCustom {
		return
	}
	r := []rune(text)
	if len(r) > 60 {
		r = r[:60]
	}
	sess.Meta.Label = string(r)
	if err := session.SaveMeta(p.cfg.Layout, sessionID, sess.Meta); err != nil {
		return
	}
	// Refresh the registry + broadcast the new title over SSE so open
	// sidebars/lists pick it up live instead of on next page load.
	if p.cfg.OnSessionMeta != nil {
		p.cfg.OnSessionMeta(sessionID)
	}
	// This branch IS "the session just got its first user message" — the
	// label was empty until now — so it is where ticket auto-create gets
	// its one chance to read the opening text.
	if p.cfg.OnFirstUserMessage != nil {
		p.cfg.OnFirstUserMessage(sessionID, text)
	}
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
	// wg only tracks background goroutines (tryGrantQueue, onAgentExit).
	// onAgentExit calls wg.Add *after* the agent reader goroutine fires,
	// so there is a window where wg reaches 0 before the last onAgentExit
	// has run releaseSlot. Poll until p.active is empty — each iteration
	// sleeps 1 ms; total wait is bounded by the number of exiting agents.
	for {
		p.mu.Lock()
		n := len(p.active)
		p.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
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
			SessionID:    e.sessID,
			AgentName:    e.agentNm,
			ProviderType: e.provType,
			ProviderName: e.provName,
			CWD:          e.cwd,
		}
		if e.state != nil {
			entry.Lifecycle = e.state.Lifecycle().String()
			entry.Substate = e.state.Current().String()
			entry.LastActive = e.state.LastActive()
		}
		if e.agent != nil {
			entry.PID = e.agent.PID()
			entry.Queued = e.agent.QueuedCount()
			entry.Respawns = e.agent.Respawns()
			entry.InFlightEvents = e.agent.InFlightEvents()
			entry.PartialText = e.agent.PartialText()
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
	SessionID      string
	AgentName      string
	ProviderType   string // resolved provider type (claude / codex / gemini)
	ProviderName   string // instance name within that type
	CWD            string // resolved workspace path, used by RouteByCWD
	PID            int
	Queued         int  // messages waiting after the current turn (RespawnQueue)
	Respawns       bool // one process per turn (codex): a dead PID between turns is normal, not a zombie
	Lifecycle      string
	Substate       string
	LastActive     time.Time
	InFlightEvents []store.TurnEvent
	// PartialText is the assistant text accumulated so far for the
	// in-flight turn (everything received via TextDelta but not yet
	// flushed by Done). Empty when no turn is mid-stream. Used by the
	// SSE snapshot so a refresh keeps the partial bubble visible.
	PartialText string
}

// Kill stops the running agent for sessionID+agentName. Idempotent if
// the agent is not currently active — returns nil in that case.
// The normal onAgentExit hook still fires, releasing the slot and
// draining the queue.
func (p *Pool) Kill(sessionID, agentName string) error {
	p.mu.Lock()
	prefix := sessionID + "::"
	var entries []*runEntry
	for k, e := range p.active {
		if strings.HasPrefix(k, prefix) {
			entries = append(entries, e)
		}
	}
	p.mu.Unlock()
	for _, e := range entries {
		if err := e.agent.Stop(); err != nil {
			log.Error().Str("session", e.sessID).Str("agent", e.agentNm).Err(err).Msg("pool.kill: agent.Stop failed")
			return err
		}
	}
	return nil
}

// PartialText returns the assistant text one agent has produced in its
// current, not-yet-flushed turn. Empty when the agent is not active or
// no turn is mid-stream.
//
// This is how partial work is captured before a kill: the last turn of
// an interrupted or turn-capped sub-agent never reaches Done, so it is
// never persisted by the normal path, and reading it here is the only
// way to hand it back to the leader instead of losing it.
func (p *Pool) PartialText(sessionID, agentName string) string {
	p.mu.Lock()
	e, ok := p.active[sessionID+"::"+agentName]
	p.mu.Unlock()
	if !ok || e.agent == nil {
		return ""
	}
	return e.agent.PartialText()
}

// ErrAgentNotActive means the requested sessionID+agentName had no live
// entry in the pool. Callers MUST treat this as a distinct outcome, not
// a failure: it covers "already finished", "never spawned", and "still
// queued". A queued sub-agent is cancelled by dropping it from the
// queue, not by killing a process that does not exist yet.
var ErrAgentNotActive = errors.New("agent not active in pool")

// KillAgent stops exactly ONE agent inside a session, leaving its
// siblings running.
//
// Kill remains "stop the whole session" — cascade teardown and the
// existing /sessions/{id}/kill route depend on that semantic, so this is
// a new method rather than a fix to Kill's ignored agentName parameter.
//
// Returns ErrAgentNotActive when no live entry matches.
func (p *Pool) KillAgent(sessionID, agentName string) error {
	p.mu.Lock()
	e, ok := p.active[sessionID+"::"+agentName]
	p.mu.Unlock()
	// Stop() blocks for up to 5s waiting on the reader goroutine, so it
	// must never run while holding p.mu — Kill releases the lock first
	// for the same reason.
	if !ok {
		return ErrAgentNotActive
	}
	if err := e.agent.Stop(); err != nil {
		log.Error().Str("session", sessionID).Str("agent", agentName).Err(err).Msg("pool.killAgent: agent.Stop failed")
		return err
	}
	return nil
}

// ReconcileDead scans active entries and Stops any whose subprocess is
// no longer alive at the OS level — a crash or external kill that the
// reader loop never saw as stdout EOF (common on Windows, where killing
// a process does not reliably close its pipe). Stop() fires the exit
// hook, releasing the slot and draining the queue, so a zombie entry
// can't wedge the pool. Cheap signal-0 probe per entry; safe to call
// from request handlers (panel open) and a periodic ticker.
func (p *Pool) ReconcileDead() {
	p.mu.Lock()
	var dead []*runEntry
	for _, e := range p.active {
		if e.agent == nil {
			continue
		}
		lc := state.LifecycleIdle
		if e.state != nil {
			lc = e.state.Lifecycle()
		}
		pid := e.agent.PID()
		alive := pid != 0 && processctl.ProcessAlive(pid)
		reap, next := reapDecision(lc, pid, alive, e.deadProbes)
		e.deadProbes = next
		if reap {
			dead = append(dead, e)
		}
	}
	p.mu.Unlock()
	for _, e := range dead {
		log.Warn().Str("session", e.sessID).Str("agent", e.agentNm).
			Int("pid", e.agent.PID()).Msg("pool.reconcile: subprocess dead, releasing slot")
		_ = e.agent.Stop()
	}
}

// reapDecision is the pure core of ReconcileDead: given an entry's
// lifecycle, its PID, whether that PID is alive, and how many consecutive
// prior ticks it looked dead, it returns whether to reap now and the
// updated dead-probe counter. Kept pure (no pool state) so the safety
// rules can be unit-tested without spawning real processes.
//
// Rules, in order:
//   - working/spawning → NEVER reap (a long turn must not be pulled out
//     from under a running process); counter resets. This is the sole
//     protection long-running work needs.
//   - alive → healthy (resident codex or any live idle process); reset.
//   - idle + (no live process: pid 0 OR dead pid) → a slot held with
//     nothing running behind it (the "dead"/"idle · kill in Ns" card that
//     never clears). Increment; reap only after it stays that way for
//     reconcileDeadThreshold consecutive ticks, so a single transient probe
//     miss can't reap a process that is actually alive. A codex that is
//     merely idle-between-turns and gets reaped here is harmless: the next
//     Send respawns it and resumes the same conversation.
func reapDecision(lc state.Lifecycle, pid int, alive bool, deadProbes int) (reap bool, nextProbes int) {
	if lc == state.LifecycleWorking || lc == state.LifecycleSpawning {
		return false, 0
	}
	if alive {
		return false, 0
	}
	n := deadProbes + 1
	return n >= reconcileDeadThreshold, n
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

// DequeueSession drops every queued request for the session regardless
// of agent name, and clears its buffered/pending input so the session
// won't execute later (even across a restart). Returns the number of
// queue entries removed. Use this from the operator UI where the caller
// only knows the session id — Dequeue requires an exact agent match,
// which the UI often can't supply.
func (p *Pool) DequeueSession(sessionID string) int {
	p.mu.Lock()
	out := p.queue[:0]
	removed := 0
	for _, q := range p.queue {
		if q.sessionID == sessionID {
			removed++
			continue
		}
		out = append(out, q)
	}
	p.queue = out
	buf := p.buffers[sessionID]
	p.mu.Unlock()
	// Clear pending input so a freed slot / restart doesn't replay it.
	if buf != nil {
		_, _ = buf.Drain()
	} else if s, err := session.Load(p.cfg.Layout, sessionID); err == nil && len(s.Meta.PendingInput) > 0 {
		s.Meta.PendingInput = nil
		_ = session.SaveMeta(p.cfg.Layout, sessionID, s.Meta)
	}
	return removed
}

// HandleExit is the public hook the factory wires into agent.OnExit.
// It frees the pool slot when a process exit means the AGENT is done —
// but for respawn-mode providers (codex) one agent spans many short-lived
// processes, so a turn-boundary exit (ExitClean) or an internal respawn
// kill (ExitRespawn) must NOT release the slot. Only a terminal reason
// (idle TTL, Stop/Kill, crash) tears the runEntry down. Without this gate
// every codex turn-end deletes the entry, and the next Send sees "no live
// subprocess" and spawns a SECOND concurrent agent (the double-spawn bug).
//
// claude (append mode) keeps the 1-agent-1-process model: every exit is
// the agent dying, so all reasons release.
func (p *Pool) HandleExit(sessionID, agentName string, reason provider.ExitReason) {
	if reason == provider.ExitError {
		p.healStaleResume(sessionID, agentName)
	}
	if reason == provider.ExitClean || reason == provider.ExitRespawn {
		p.mu.Lock()
		entry, ok := p.active[sessionKey(sessionID, agentName)]
		respawns := ok && entry.agent != nil && entry.agent.Respawns()
		p.mu.Unlock()
		if respawns {
			// Turn boundary, not agent death. Keep the slot; the agent
			// either respawns now (queued message) or idles until its TTL
			// fires ExitIdle, which DOES release. Nudge SSE so the panel's
			// lifecycle/queued counts stay live across the turn boundary.
			if ok {
				p.notifyLifecycle(entry.ctx, entry, sessionID, agentName)
			}
			return
		}
	}
	p.onAgentExit(sessionID, agentName)
	// After the slot is released, decide whether this death deserves a
	// restart and tell the agent what happened. Ordered after
	// onAgentExit so the restart competes for a free slot like any other
	// spawn rather than racing the one it just vacated.
	p.recoverFromExit(sessionID, agentName, reason)
}

// recoverFromExit restarts an agent that died for no stated reason, and
// leaves the session an explanation either way.
//
// Detection already existed; what was missing was the response. From the
// session's point of view an unexplained death looked like the agent
// stopping mid-sentence — no message, no continuation, and the next user
// turn began with a fresh process that had no idea anything had gone
// wrong.
func (p *Pool) recoverFromExit(sessionID, agentName string, reason provider.ExitReason) {
	key := sessionKey(sessionID, agentName)

	// An explained exit clears the history: a clean stop today must not
	// eat into the restart budget of an unrelated crash next week.
	if reason != provider.ExitError && reason != provider.ExitOOM {
		p.mu.Lock()
		p.clearCrashesLocked(key)
		p.mu.Unlock()
		return
	}

	l := log.With().
		Str("component", "pool").
		Str("session", sessionID).
		Str("agent", agentName).
		Logger()

	// A memory kill is never retried — the same work would hit the same
	// ceiling. The agent is told instead, with the numbers, so it can
	// propose a smaller approach.
	if reason == provider.ExitOOM {
		p.mu.Lock()
		p.clearCrashesLocked(key)
		p.mu.Unlock()
		l.Warn().Msg("pool.recover: agent stopped for memory; not restarting")
		// The measured peak and the ceiling live on ExitDetail, which
		// travels a different hook (OnExitDetail → the spawn log). Rather
		// than thread that value through a second path just for this
		// sentence, the notice states the cause and points at where the
		// numbers already are.
		p.notifySession(sessionID, agentName, oomNotice(
			"It went over the memory limit set for it; Recent Spawns has the measured figure."))
		return
	}

	p.mu.Lock()
	attempt := p.noteCrashLocked(key, time.Now())
	p.mu.Unlock()

	if !ShouldRespawn(reason, attempt-1) {
		l.Error().Int("attempts", attempt).
			Msg("pool.recover: repeated unexplained exits; giving up")
		p.notifySession(sessionID, agentName, crashNotice("repeated unexpected exits", attempt, true))
		return
	}

	l.Warn().Int("attempt", attempt).Msg("pool.recover: unexplained exit; restarting")
	// Sending a system turn both delivers the explanation AND triggers the
	// spawn: Send is the pool's one entry point for starting an agent, so
	// the restart goes through the same slot accounting, queueing, and
	// admission checks as any other. A separate spawn path here would be a
	// second way to start an agent, and the two would drift.
	p.notifySession(sessionID, agentName, crashNotice("unexpected exit", attempt, false))
}

// notifySession delivers a system turn to the session, which also brings
// the agent back if it is not running. Best-effort: a failed notice must
// not itself become an error path.
func (p *Pool) notifySession(sessionID, agentName, msg string) {
	if err := p.Send(context.Background(), sessionID, agentName, "recover", "system", msg); err != nil {
		log.Warn().Err(err).
			Str("component", "pool").
			Str("session", sessionID).
			Msg("pool.recover: could not deliver the notice")
	}
}

// healStaleResume clears the CLI session id when a --resume spawn failed
// because the CLI couldn't find the conversation, so the next is fresh.
func (p *Pool) healStaleResume(sessionID, agentName string) {
	p.mu.Lock()
	entry, ok := p.active[sessionKey(sessionID, agentName)]
	p.mu.Unlock()
	if !ok || entry.agent == nil {
		return
	}
	if entry.agent.SpawnResumeID() == "" {
		return // fresh spawn — nothing stale to clear
	}
	if !provider.IsResumeNotFound(entry.agent.StderrTail()) {
		return
	}
	if err := session.SetCLISessionID(p.cfg.Layout, sessionID, agentName, ""); err != nil {
		log.Warn().Str("session", sessionID).Str("agent", agentName).Err(err).
			Msg("pool: failed clearing stale resume id")
		return
	}
	log.Info().Str("session", sessionID).Str("agent", agentName).
		Msg("pool: cleared stale CLI resume id (No conversation found) — next spawn starts fresh")
}

// sessionHasCLISession reports whether any agent already captured a CLI
// session id (i.e. a resumable conversation exists for this session).
func sessionHasCLISession(s session.Session) bool {
	for _, a := range s.Agents {
		if a.CLISessionID != "" {
			return true
		}
	}
	return false
}

// sessionKey is the canonical map key for an active agent.
func sessionKey(sessionID, agentName string) string {
	return sessionID + "::" + agentName
}
