package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

// Agent owns one running subprocess. Lifecycle:
//
//	NewAgent(...)        — constructed, not yet started
//	Start(ctx)           — spawn subprocess, kick off reader + idle timer
//	Send("...")          — write a user message into stdin
//	Stop()               — kill subprocess, wait for reader to drain
//
// Idle TTL: while no parser event arrives for IdleTimeout, the agent
// kills its own subprocess. State machine is reset to Idle so callers
// can spawn a fresh process on the next message (with --resume if a
// CLI session ID was captured).
//
// Callers (the pool) treat Agent as one-shot per spawn — kill returns
// the agent to "ready to spawn again" rather than reusing the process.
type Agent struct {
	cfg     Options
	parser  event.Parser
	state   *state.Machine
	store   *store.Store
	spawner Spawner

	mu      sync.Mutex
	proc    Process
	cancel  context.CancelFunc
	running bool
	// rootCtx is the context passed to Start; used by RespawnOnSend to
	// re-spawn with a fresh subcontext without needing an external ctx arg.
	rootCtx context.Context

	// done is closed when the reader goroutine exits — Stop() waits on it.
	done chan struct{}

	// resumeID is the CLI session ID captured from SessionStart events.
	// Persisted via store; mirrored here so re-spawn (without going
	// through pool.Reload) can pass --resume.
	resumeID string

	// onEvent is fired for every parsed event — pool / SSE consumers
	// can subscribe to react to state changes (queue draining, dashboard
	// streaming). Optional.
	onEvent func(event.AgentEvent)

	// onExit is fired when the subprocess exits (either via Stop or
	// idle TTL). Optional.
	onExit func(reason ExitReason)

	// exitReasonSet ensures OnExit fires at most once per spawn, even
	// when both the idle goroutine and the reader-exit path race.
	exitReasonSet bool

	// pendingQueue holds messages that arrived while a RespawnOnSend
	// (codex) turn was in flight, in FIFO order. Codex is one-shot per
	// spawn, so we can't respawn mid-turn (it would orphan the running
	// process and stack subprocesses past MaxConcurrent). Each queued
	// message runs as its own turn after the current one ends — every
	// Enter is processed, none are dropped. drainPending pops the head.
	pendingQueue []string
	// turnActive is true for RespawnOnSend agents between a respawn and
	// its turn completing (lifecycle returns to idle). Gates whether a
	// fresh Send respawns now or just appends to pendingQueue.
	turnActive bool

	// respawning is set while respawnWithMessage is tearing down the
	// current process to start a fresh turn. The reader's ctx.Done path
	// reads it to fire ExitRespawn (agent lives on) instead of
	// ExitStopped (agent dead), so the pool keeps the slot. Cleared once
	// the new process is wired up.
	respawning bool

	// stopped latches once Stop() runs. A respawn-mode (codex) agent can
	// have a drainPending goroutine in flight when Stop is called; without
	// this latch that goroutine would respawn a fresh process AFTER the
	// kill, resurrecting an agent the pool already released. respawnWithMessage
	// and drainPending bail when stopped is set.
	stopped bool

	// activityCh is signalled on every stdout line so the grace-period
	// watcher can cancel the kill when the subprocess produces output
	// during the KillAfterIdle window.
	activityCh chan struct{}
}

// ExitReason classifies why the subprocess ended. The pool uses this
// to decide whether to drain queued messages immediately.
type ExitReason int

const (
	ExitClean   ExitReason = iota // subprocess returned normally
	ExitIdle                      // idle TTL killed it
	ExitStopped                   // Stop() was called
	ExitError                     // wait returned an error
	// ExitRespawn is the internal kill of the current process by
	// respawnWithMessage so a fresh turn can spawn. The AGENT lives on —
	// only one of its short-lived processes ended. The pool uses this to
	// keep the slot for respawn-mode (codex) agents across turn
	// boundaries instead of treating each turn-end as agent death.
	ExitRespawn
)

// Options is the constructor argument. ParserFactory returns a fresh
// parser per spawn (parsers carry per-stream state — block index map
// and so on — so we can't reuse one across processes).
type Options struct {
	Workspace   string
	ResumeID    string
	IdleTimeout time.Duration
	// KillAfterIdle is the extra grace period after IdleTimeout fires
	// before the subprocess is actually killed. 0 = kill immediately.
	// During the grace period, new output from the subprocess resets
	// the cycle (grace is cancelled and IdleTimeout restarts).
	KillAfterIdle time.Duration
	ParserFactory func() event.Parser
	Spawner       Spawner
	Store         *store.Store
	State         *state.Machine

	OnEvent func(event.AgentEvent)
	OnExit  func(reason ExitReason)

	// Instance is the per-instance config the spawner should consult
	// every spawn (hook intent, env, …). Forwarded into SpawnOptions
	// so the spawn package is the only place that reads the registry.
	Instance *Instance
	// GateBinary is the absolute path to <app>-gate, resolved once by
	// the factory and threaded through every spawn so the spawner can
	// write hook configs without re-resolving.
	GateBinary string
	// Preset is the system prompt content forwarded to the spawner as
	// --append-system-prompt (or equivalent). Stripped from spawn logs.
	Preset string
	// ExtraEnv merges into the subprocess env on every spawn. Used by
	// per-channel transports (Slack, HTTP) that need to inject auth
	// tokens or routing keys.
	ExtraEnv []string
	// MessageEncoder formats a user message before writing to stdin.
	// nil = default Claude stream-json envelope. Ignored unless SendMode
	// is SendAppend.
	MessageEncoder func(text string) string
	// SendMode decides what Send() does with a user message. See SendMode
	// docs. Zero value (SendAppend) keeps the long-lived-stdin behaviour.
	SendMode SendMode
	// MaxTurns caps agentic turns on each spawn (--max-turns). 0 = no cap.
	// Forwarded verbatim into SpawnOptions.
	MaxTurns int
}

// SendMode controls how an Agent delivers a user message to its CLI.
// Different runtimes have different process models:
type SendMode int

const (
	// SendAppend writes the message to the long-lived subprocess stdin.
	// The process persists across turns and queues input itself. Used by
	// claude (stream-json over stdin). No respawn, no per-message process.
	SendAppend SendMode = iota

	// SendRespawnQueue is for one-shot-per-invocation CLIs (codex): each
	// turn is a fresh subprocess with the prompt as an argv arg. While a
	// turn runs, a new Send is QUEUED (parked as pendingMsg) and respawns
	// once the turn finishes — never spawning a second concurrent process.
	// This keeps one process per session and respects MaxConcurrent.
	SendRespawnQueue

	// SendSpawnEach spawns a brand-new subprocess for EVERY message, in
	// parallel, with no queueing. Each counts against the pool slot/queue
	// like an independent run. Use only for truly stateless one-shot tools
	// where concurrent fan-out is desired. (Not used by built-in providers.)
	SendSpawnEach
)

// respawns reports whether this mode spawns a fresh process per turn
// (vs. SendAppend's persistent stdin).
func (m SendMode) respawns() bool { return m == SendRespawnQueue || m == SendSpawnEach }

// Respawns reports whether this agent spawns a fresh process per turn
// (codex) rather than holding one long-lived process (claude). The pool
// reads it to decide whether a process exit means the agent died (claude:
// yes) or just a turn ended (codex: no — keep the slot).
func (a *Agent) Respawns() bool { return a.cfg.SendMode.respawns() }

// String renders a SendMode as its config-key value. Inverse of
// ParseSendMode. Used by the providers UI to show the current selection.
func (m SendMode) String() string {
	switch m {
	case SendRespawnQueue:
		return "queue"
	case SendSpawnEach:
		return "spawn"
	default:
		return "append"
	}
}

// ParseSendMode maps a config string to a SendMode. Unknown / empty
// strings return (SendAppend, false) so callers fall back to the
// per-type default rather than silently forcing append.
func ParseSendMode(s string) (SendMode, bool) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "append":
		return SendAppend, true
	case "queue":
		return SendRespawnQueue, true
	case "spawn":
		return SendSpawnEach, true
	default:
		return SendAppend, false
	}
}

// New builds an Agent from Options. Doesn't spawn — call Start.
func New(opt Options) *Agent {
	return &Agent{
		cfg:        opt,
		state:      opt.State,
		store:      opt.Store,
		spawner:    opt.Spawner,
		onEvent:    opt.OnEvent,
		onExit:     opt.OnExit,
		resumeID:   opt.ResumeID,
		done:       make(chan struct{}),
		activityCh: make(chan struct{}, 1),
	}
}

// Running reports whether the subprocess is currently alive.
func (a *Agent) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// Start spawns the subprocess and begins consuming its stdout. ctx
// being canceled tears the agent down. Returns nil immediately on
// success; the reader runs in a background goroutine.
func (a *Agent) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("agent already running")
	}
	a.rootCtx = ctx
	a.parser = a.cfg.ParserFactory()

	// Respawn-per-turn providers (codex) take the prompt as a positional
	// argv arg and run one-shot per message. Spawning here with no
	// prompt would launch a process that immediately errors (codex exec
	// with no [PROMPT] exits 1) — pure waste. Defer the first spawn to
	// the first Send(), which carries the prompt via respawnWithMessage.
	if a.cfg.SendMode.respawns() {
		a.running = true
		a.mu.Unlock()
		return nil
	}

	subCtx, cancel := context.WithCancel(ctx)
	proc, err := a.spawner.Spawn(subCtx, SpawnOptions{
		Workspace:  a.cfg.Workspace,
		ResumeID:   a.resumeID,
		ExtraEnv:   a.cfg.ExtraEnv,
		Instance:   a.cfg.Instance,
		GateBinary: a.cfg.GateBinary,
		Preset:     a.cfg.Preset,
		MaxTurns:   a.cfg.MaxTurns,
	})
	if err != nil {
		cancel()
		a.mu.Unlock()
		return err
	}
	a.proc = proc
	a.cancel = cancel
	a.running = true
	a.done = make(chan struct{})
	a.mu.Unlock()

	go a.run(subCtx)
	return nil
}

// Send writes one user message line to the subprocess stdin. The
// message is wrapped as a stream-json user message — claude expects
// `{"type":"user","message":{"role":"user","content":"..."}}` plus
// newline when invoked with --input-format stream-json.
//
// Caller is also expected to AppendUserTurn into the store so
// conversation.jsonl reflects the message; we don't double-write here
// because some transports (replay tests) skip storage.
func (a *Agent) Send(text string) error {
	switch a.cfg.SendMode {
	case SendRespawnQueue:
		// Queue mid-turn, respawn once when free. respawnWithMessage
		// handles the turnActive gate.
		return a.respawnWithMessage(text)
	case SendSpawnEach:
		// Always a fresh process, even mid-turn. No queue gate: clear
		// turnActive so respawnWithMessage spawns immediately. Parallel
		// turns are the caller's intent for this mode.
		a.mu.Lock()
		a.turnActive = false
		a.mu.Unlock()
		return a.respawnWithMessage(text)
	}
	// SendAppend: write to the persistent subprocess stdin.
	// Release a.mu before I/O so callers of drainPending (which also
	// acquires a.mu) cannot deadlock if the stdin pipe blocks.
	a.mu.Lock()
	if !a.running || a.proc == nil {
		a.mu.Unlock()
		return errors.New("agent not running")
	}
	var payload string
	if a.cfg.MessageEncoder != nil {
		payload = a.cfg.MessageEncoder(text)
	} else {
		payload = fmt.Sprintf(
			`{"type":"user","message":{"role":"user","content":%s}}`,
			jsonString(text),
		)
	}
	proc := a.proc
	a.mu.Unlock()
	log.Debug().Str("payload", payload).Msg("agent.send: writing to stdin")
	_, err := proc.Stdin().Write([]byte(payload + "\n"))
	return err
}

// respawnWithMessage stops the current process (if any) and spawns a new one
// with text as the InitialMessage positional arg. Used by codex which is
// one-shot per invocation.
// drainPending clears the turn-active flag and, if a message was parked
// while the turn ran, respawns once with it. Called from the reader on
// Done/Error. Runs the respawn in a goroutine so the reader loop (which
// holds no lock here but is mid-iteration) isn't blocked by the spawn.
func (a *Agent) drainPending() {
	a.mu.Lock()
	a.turnActive = false
	if a.stopped || len(a.pendingQueue) == 0 {
		a.mu.Unlock()
		return
	}
	next := a.pendingQueue[0]
	a.pendingQueue = a.pendingQueue[1:]
	a.mu.Unlock()
	log.Debug().Int("remaining", len(a.pendingQueue)).Msg("agent.drain: running next queued message after turn completion")
	go func() {
		if err := a.respawnWithMessage(next); err != nil {
			log.Warn().Err(err).Msg("agent.drain: respawn for queued message failed")
		}
	}()
}

func (a *Agent) respawnWithMessage(text string) error {
	a.mu.Lock()
	// Agent was stopped (Kill / Stop). Do not resurrect it — a late
	// drainPending goroutine or a Send racing the kill must not spawn a
	// fresh process the pool already let go.
	if a.stopped {
		a.mu.Unlock()
		return errors.New("agent stopped")
	}
	// A turn is already running. Codex is one-shot per spawn, so we must
	// NOT kill+respawn now — that orphans the live process and stacks up
	// subprocesses past MaxConcurrent. Append to the FIFO queue; each
	// message runs as its own turn after the current one ends (drainPending
	// pops the head). Every Enter is processed in order — none dropped.
	if a.turnActive {
		a.pendingQueue = append(a.pendingQueue, text)
		a.mu.Unlock()
		log.Debug().Int("queued", len(a.pendingQueue)).Msg("agent.respawn: turn in flight, message queued")
		return nil
	}
	a.turnActive = true
	ctx := a.rootCtx
	resumeID := a.resumeID
	// Kill current process if alive. Uses terminateProc so a hung
	// subprocess (e.g. codex spawned without a prompt, blocking on
	// stdin) doesn't freeze the respawn at <-done — same Windows
	// stdout-EOF issue Stop() guards against.
	//
	// respawning gates the old reader's ctx.Done exit: it fires
	// ExitRespawn (agent lives, pool keeps slot) instead of ExitStopped
	// (agent dead, pool frees slot).
	if a.running && a.proc != nil {
		a.respawning = true
		if a.cancel != nil {
			a.cancel()
		}
		proc := a.proc
		done := a.done
		a.mu.Unlock()
		terminateProc(proc, done)
		a.mu.Lock()
		a.respawning = false
	}
	if ctx == nil {
		a.turnActive = false
		a.mu.Unlock()
		return errors.New("agent not started")
	}
	a.parser = a.cfg.ParserFactory()
	a.exitReasonSet = false

	log.Debug().Str("message", text).Str("resume_id", resumeID).Msg("agent.respawn: spawning with initial message")

	subCtx, cancel := context.WithCancel(ctx)
	proc, err := a.spawner.Spawn(subCtx, SpawnOptions{
		Workspace:      a.cfg.Workspace,
		ResumeID:       resumeID,
		ExtraEnv:       a.cfg.ExtraEnv,
		Instance:       a.cfg.Instance,
		GateBinary:     a.cfg.GateBinary,
		Preset:         a.cfg.Preset,
		InitialMessage: text,
		MaxTurns:       a.cfg.MaxTurns,
	})
	if err != nil {
		// Spawn failed — clear turnActive so a retry isn't parked forever.
		a.turnActive = false
		cancel()
		a.mu.Unlock()
		return err
	}
	a.proc = proc
	a.cancel = cancel
	a.running = true
	a.done = make(chan struct{})
	a.mu.Unlock()

	// Respawn = a brand-new subprocess from the FE's perspective; flip
	// the state machine to Spawning so the lifecycle hook broadcasts
	// the transition (otherwise codex/respawn-on-send providers leave
	// the badge stuck at idle until the first event arrives).
	if a.state != nil {
		a.state.MarkSpawning()
	}

	go a.run(subCtx)
	return nil
}

// Stop kills the subprocess and waits for the reader to exit.
// Idempotent: calling on a stopped agent returns nil. The accompanying
// store is flushed so partial assistant turns don't disappear.
// terminateProc kills proc and waits for the reader loop to signal done.
// The reader (run) selects on ctx.Done(), so once the caller has
// cancelled the spawn ctx it exits promptly even if the stdout pipe
// hasn't EOF'd (Windows Kill does not reliably EOF) — no <-done hang.
// A short timeout still guards against any unexpected stall. Safe with
// a nil proc.
func terminateProc(proc Process, done <-chan struct{}) {
	pid := 0
	if proc != nil {
		pid = proc.Pid()
		_ = proc.Stdin().Close()
		_ = proc.Kill()
	}
	if done == nil {
		return
	}
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		log.Warn().Int("pid", pid).Msg("agent.terminate: reader did not exit within 5s after kill; proceeding")
	}
}

func (a *Agent) Stop() error {
	a.mu.Lock()
	// stopped latches first so any in-flight drainPending / Send bails out
	// instead of respawning a fresh process after the kill.
	a.stopped = true
	// respawn-mode (codex) is one process per turn: between turns the
	// process is dead but the AGENT still owns its pool slot. Its reader
	// exits via ExitClean (which the pool deliberately ignores to keep the
	// slot across turns), so a Stop MUST fire ExitStopped itself or the
	// slot never frees (badge stuck, Kill a no-op). We force that fire
	// below regardless of which teardown branch ran.
	respawn := a.cfg.SendMode.respawns()
	running := a.running
	proc := a.proc
	cancel := a.cancel
	done := a.done
	a.turnActive = false
	a.pendingQueue = nil
	if respawn {
		// The last turn's ExitClean already consumed exitReasonSet; reset so
		// the forced ExitStopped below actually fires the hook.
		a.exitReasonSet = false
	}
	a.mu.Unlock()

	if running {
		if cancel != nil {
			cancel()
		}
		terminateProc(proc, done)
		a.mu.Lock()
		a.turnActive = false
		a.pendingQueue = nil
		a.mu.Unlock()
	}
	if a.store != nil {
		_ = a.store.Flush()
	}
	if respawn {
		// claude (append mode) frees its slot via the reader's ctx.Done →
		// ExitStopped path; only respawn-mode needs the explicit fire here.
		//
		// Reset exitReasonSet AFTER teardown: a between-turns reader exit
		// fires ExitClean (which the pool ignores to keep the slot), which
		// would otherwise latch exitReasonSet and swallow our ExitStopped.
		// Resetting here, once the reader has finished, guarantees the
		// forced fire reaches onAgentExit and frees the slot.
		a.mu.Lock()
		a.exitReasonSet = false
		a.mu.Unlock()
		a.exitReason(ExitStopped)
	}
	return nil
}

// ResumeID returns the captured CLI session ID, or "" if SessionStart
// has not arrived yet. Pool reads this when re-spawning after idle
// kill so claude --resume picks up the same conversation.
func (a *Agent) ResumeID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resumeID
}

// SpawnResumeID returns the --resume id this spawn started with, so the
// pool can tell a fresh-spawn failure from a stale-resume failure.
func (a *Agent) SpawnResumeID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ResumeID
}

// StderrTail returns the tail of the subprocess's stderr ("" if not
// captured). Safe after exit — the dead process still holds its buffer.
func (a *Agent) StderrTail() string {
	a.mu.Lock()
	p := a.proc
	a.mu.Unlock()
	if t, ok := p.(interface{ StderrTail() string }); ok {
		return t.StderrTail()
	}
	return ""
}

// IsResumeNotFound reports whether output indicates a --resume id the
// CLI couldn't find, so the pool can clear the stale id and respawn fresh.
func IsResumeNotFound(s string) bool {
	return strings.Contains(strings.ToLower(s), "no conversation found")
}

// PID returns the OS pid of the current subprocess, or 0 if not
// running. Pool reads this after Start so the spawn log captures the
// real pid (Build runs before Start, so the start event written there
// can't know the pid yet).
func (a *Agent) PID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return 0
	}
	return a.proc.Pid()
}

// QueuedCount returns how many messages are waiting to run after the
// current turn (RespawnQueue providers). 0 for append-mode agents.
// Surfaced in the Process panel so the operator sees the backlog.
func (a *Agent) QueuedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pendingQueue)
}

// Binary returns the resolved binary path of the running subprocess.
// Empty when not running or when the spawner is a test fake.
func (a *Agent) Binary() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return ""
	}
	return a.proc.Binary()
}

// Argv returns the argument vector of the running subprocess. Empty
// when not running or when the spawner is a test fake.
func (a *Agent) Argv() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return nil
	}
	return a.proc.Argv()
}

// InFlightEvents returns events buffered in the current in-progress turn
// (tool_use, tool_result, thinking) that have not yet been flushed to
// disk. Returns nil when no turn is active or store is not wired.
func (a *Agent) InFlightEvents() []store.TurnEvent {
	if a.store == nil {
		return nil
	}
	return a.store.InFlightEvents()
}

// PartialText returns the assistant text accumulated so far for the
// in-flight turn. Empty when no turn is active or store is not wired.
// The SSE snapshot endpoint uses this so a refresh mid-stream repaints
// the partial bubble instead of waiting for the next delta.
func (a *Agent) PartialText() string {
	if a.store == nil {
		return ""
	}
	return a.store.PartialText()
}

// run is the reader goroutine. Reads lines from stdout, parses each,
// applies to state + store, fires the OnEvent hook, resets the idle
// timer, and detects subprocess exit. Stops when stdout returns EOF or
// ctx fires.
func (a *Agent) run(ctx context.Context) {
	defer close(a.done)
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	scanner := bufio.NewScanner(a.proc.Stdout())
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// Read lines in a dedicated goroutine and feed them over a channel.
	// On Windows, killing the subprocess does NOT reliably EOF the stdout
	// pipe, so a bare `for scanner.Scan()` can block forever after Kill —
	// freezing Stop()'s <-done wait and, through it, the whole server.
	// By selecting between this channel and ctx.Done(), the reader loop
	// exits promptly on cancel (Stop/respawn cancel the ctx) even while
	// the orphaned Scan goroutine is still parked; it unparks and exits
	// when the OS eventually tears the pipe down after process reap.
	lineCh := make(chan string)
	go func() {
		defer close(lineCh)
		for scanner.Scan() {
			select {
			case lineCh <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	idle := time.NewTimer(a.cfg.IdleTimeout)
	defer idle.Stop()

	// Two-stage idle kill:
	//   Stage 1 — IdleTimeout fires (no stdout for N seconds): if
	//             KillAfterIdle == 0, kill immediately (legacy behaviour).
	//             Otherwise enter grace period.
	//   Stage 2 — KillAfterIdle expires during grace: kill subprocess.
	//             New stdout arriving during grace resets the cycle.
	go func() {
		for {
			select {
			case <-idle.C:
				if a.cfg.KillAfterIdle <= 0 {
					a.mu.Lock()
					proc := a.proc
					a.mu.Unlock()
					if proc != nil {
						_ = proc.Kill()
					}
					a.exitReason(ExitIdle)
					return
				}
				// Grace period: new output cancels the kill.
				grace := time.NewTimer(a.cfg.KillAfterIdle)
				select {
				case <-grace.C:
					a.mu.Lock()
					proc := a.proc
					a.mu.Unlock()
					if proc != nil {
						_ = proc.Kill()
					}
					a.exitReason(ExitIdle)
					grace.Stop()
					return
				case <-a.activityCh:
					// Activity during grace — idle timer already reset
					// by the scanner loop; restart the outer select.
					grace.Stop()
				case <-ctx.Done():
					grace.Stop()
					return
				}
			case <-a.activityCh:
				// Drain stale wakeup that arrived before idle.C fired.
			case <-ctx.Done():
				return
			}
		}
	}()

	// toolInFlight is true from ToolUse until the matching ToolResult.
	// While a tool is running the subprocess stdout goes silent (the
	// tool itself is executing), so we must not let the idle timer fire
	// and kill the process. The timer is stopped on ToolUse and
	// restarted on ToolResult so the normal idle-kill still applies once
	// the tool finishes.
	toolInFlight := false

	for {
		var line string
		select {
		case l, ok := <-lineCh:
			if !ok {
				// stdout closed / EOF — normal end of stream.
				goto drained
			}
			line = l
		case <-ctx.Done():
			// Killed (Stop / respawn). Exit immediately. Do NOT block on
			// proc.Wait() here — on Windows Wait() can hang for seconds
			// waiting for stdout/stderr copies to finish while the Scan
			// goroutine is still parked on a not-yet-EOF pipe, which would
			// freeze Stop()'s <-done wait. Reap the process asynchronously
			// instead so the reader (and thus done) closes immediately.
			go func() { _ = a.proc.Wait() }()
			// Fire the exit hook so the pool releases the slot and drains
			// the queue. Without this a preempt/Stop leaves the slot held
			// forever — the queued session never spawns (the "stuck idle,
			// queue never runs" bug). exitReason is idempotent.
			//
			// If this cancel came from respawnWithMessage (codex turn
			// boundary), report ExitRespawn so the pool keeps the slot for
			// the incoming process instead of tearing the runEntry down and
			// double-spawning on the next Send.
			a.mu.Lock()
			respawning := a.respawning
			a.mu.Unlock()
			if respawning {
				a.exitReason(ExitRespawn)
			} else {
				a.exitReason(ExitStopped)
			}
			return
		}

		ev, err := a.parser.Parse(line)
		if err != nil {
			// One bad line shouldn't tank the agent. Log+continue is
			// the policy the design specifies; we surface as Error
			// event so the store + UI still see it.
			ev = event.AgentEvent{Type: event.Error, ErrorMsg: err.Error(), Raw: line}
		}

		switch ev.Type {
		case event.ToolUse:
			// Tool about to execute — stdout will be silent. Stop the
			// idle timer until the result comes back.
			if !toolInFlight {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				toolInFlight = true
			}
		case event.ToolResult:
			// Tool finished — restart idle timer from now.
			toolInFlight = false
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(a.cfg.IdleTimeout)
			select {
			case a.activityCh <- struct{}{}:
			default:
			}
		case event.Done, event.Error:
			// Turn ended (normally or via error) — always reset
			// toolInFlight so a crash mid-tool doesn't leave the idle
			// timer stopped forever.
			toolInFlight = false
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(a.cfg.IdleTimeout)
			select {
			case a.activityCh <- struct{}{}:
			default:
			}
			// RespawnOnSend (codex) turn finished. Mark the turn idle and
			// drain any message that arrived mid-turn, respawning exactly
			// once. This is what prevents codex spam from stacking
			// subprocesses past MaxConcurrent.
			a.drainPending()
		default:
			// For every other line reset the idle timer only when no
			// tool is in flight — tool execution keeps stdout silent
			// and we must not accidentally restart the timer mid-tool.
			if !toolInFlight {
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(a.cfg.IdleTimeout)
				select {
				case a.activityCh <- struct{}{}:
				default:
				}
			}
		}

		if ev.Type == event.SessionStart && ev.SessionID != "" {
			a.mu.Lock()
			a.resumeID = ev.SessionID
			a.mu.Unlock()
		}
		// Persist BEFORE flipping the state machine so anything wired
		// to lifecycle-idle (e.g. push notification dispatch) reads a
		// conversation.jsonl that already has the just-finished
		// assistant turn. Previous order fired state.Apply (which
		// triggers the lifecycle hook synchronously) before store.Apply
		// wrote the JSONL, so the notification body preview lagged by
		// one turn.
		if a.store != nil {
			_, _ = a.store.Apply(ev)
		}
		a.state.Apply(ev)
		if a.onEvent != nil {
			a.onEvent(ev)
		}
	}

drained:
	// Reader exited — wait for process so resources are reaped.
	waitErr := a.proc.Wait()
	a.state.MarkIdle()

	a.mu.Lock()
	already := a.exitReasonSet
	a.mu.Unlock()
	if already {
		// idle goroutine already fired the hook
		return
	}
	reason := ExitClean
	if waitErr != nil && !isCleanExitErr(waitErr) {
		reason = ExitError
	}
	// Log the raw wait error + reason so an unexpected crash (subprocess
	// dies right after spawn before emitting session_start) is visible.
	// Clean exits log at debug; abnormal exits log at warn so they pop
	// out of the firehose.
	lvl := log.Debug
	if reason == ExitError {
		lvl = log.Warn
	}
	ev := lvl().
		Str("component", "agent").
		Int("pid", a.PID()).
		Int("reason", int(reason)).
		Str("reason_name", exitReasonName(reason))
	if waitErr != nil {
		ev = ev.Str("wait_err", waitErr.Error())
	}
	// On abnormal exit, log the exit code + stderr tail so failures are
	// diagnosable instead of a blank "agent error: ".
	if reason == ExitError {
		if ec, ok := waitErr.(interface{ ExitCode() int }); ok {
			ev = ev.Int("exit_code", ec.ExitCode())
		}
		if st, ok := a.proc.(interface{ StderrTail() string }); ok {
			if tail := st.StderrTail(); tail != "" {
				ev = ev.Str("stderr_tail", tail)
			}
		}
	}
	ev.Msg("agent.reader: subprocess exited")
	a.exitReason(reason)
}

// exitReasonName mirrors the ExitReason iota for log lines. Kept local
// to agent.go so the reader path doesn't need to import the pool's
// stringifier.
func exitReasonName(r ExitReason) string {
	switch r {
	case ExitClean:
		return "clean"
	case ExitIdle:
		return "idle_ttl"
	case ExitStopped:
		return "stopped"
	case ExitError:
		return "error"
	case ExitRespawn:
		return "respawn"
	}
	return "unknown"
}

// exitReason fires the OnExit hook at most once per spawn. Idempotent
// because both the idle timer and the reader-exit path call it.
func (a *Agent) exitReason(r ExitReason) {
	a.mu.Lock()
	if a.exitReasonSet {
		a.mu.Unlock()
		return
	}
	a.exitReasonSet = true
	hook := a.onExit
	a.mu.Unlock()
	if hook != nil {
		hook(r)
	}
}

// jsonString escapes s into a JSON string literal so we can embed it
// in the stream-json envelope without pulling encoding/json for one
// field. Quotes, backslashes, and control chars get the standard
// escapes; everything else passes through.
func jsonString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				out = append(out, []byte(fmt.Sprintf("\\u%04x", r))...)
			} else {
				out = append(out, []byte(string(r))...)
			}
		}
	}
	out = append(out, '"')
	return string(out)
}

// isCleanExitErr returns true for ctx-cancellation errors that we
// consider a normal shutdown (idle kill, Stop()). os/exec wraps the
// signal into ExitError; killing on Windows looks identical to a
// crash, so we match by message rather than syscall.
func isCleanExitErr(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Killed processes report ExitError on both platforms; treat as
	// clean since we asked for it.
	return false
}
