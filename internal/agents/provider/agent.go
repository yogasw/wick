package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

	// activityCh is signalled on every stdout line so the grace-period
	// watcher can cancel the kill when the subprocess produces output
	// during the KillAfterIdle window.
	activityCh chan struct{}
}

// ExitReason classifies why the subprocess ended. The pool uses this
// to decide whether to drain queued messages immediately.
type ExitReason int

const (
	ExitClean ExitReason = iota // subprocess returned normally
	ExitIdle                    // idle TTL killed it
	ExitStopped                 // Stop() was called
	ExitError                   // wait returned an error
)

// Options is the constructor argument. ParserFactory returns a fresh
// parser per spawn (parsers carry per-stream state — block index map
// and so on — so we can't reuse one across processes).
type Options struct {
	Workspace     string
	ResumeID      string
	IdleTimeout   time.Duration
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
	a.parser = a.cfg.ParserFactory()

	subCtx, cancel := context.WithCancel(ctx)
	proc, err := a.spawner.Spawn(subCtx, SpawnOptions{
		Workspace: a.cfg.Workspace,
		ResumeID:  a.resumeID,
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
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running || a.proc == nil {
		return errors.New("agent not running")
	}
	payload := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":%s}}`,
		jsonString(text),
	)
	_, err := a.proc.Stdin().Write([]byte(payload + "\n"))
	return err
}

// Stop kills the subprocess and waits for the reader to exit.
// Idempotent: calling on a stopped agent returns nil. The accompanying
// store is flushed so partial assistant turns don't disappear.
func (a *Agent) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	proc := a.proc
	cancel := a.cancel
	done := a.done
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if proc != nil {
		_ = proc.Stdin().Close()
		_ = proc.Kill()
	}
	<-done
	if a.store != nil {
		_ = a.store.Flush()
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

	for scanner.Scan() {
		line := scanner.Text()

		if !idle.Stop() {
			// Drain the channel if the timer already fired.
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(a.cfg.IdleTimeout)
		// Signal activity so any active grace period is cancelled.
		select {
		case a.activityCh <- struct{}{}:
		default:
		}

		ev, err := a.parser.Parse(line)
		if err != nil {
			// One bad line shouldn't tank the agent. Log+continue is
			// the policy the design specifies; we surface as Error
			// event so the store + UI still see it.
			ev = event.AgentEvent{Type: event.Error, ErrorMsg: err.Error(), Raw: line}
		}
		if ev.Type == event.SessionStart && ev.SessionID != "" {
			a.mu.Lock()
			a.resumeID = ev.SessionID
			a.mu.Unlock()
		}
		a.state.Apply(ev)
		if a.store != nil {
			_, _ = a.store.Apply(ev)
		}
		if a.onEvent != nil {
			a.onEvent(ev)
		}
	}

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
	a.exitReason(reason)
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
