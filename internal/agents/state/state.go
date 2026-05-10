// Package state holds the per-agent runtime state machine. The
// machine is a tiny FSM driven by AgentEvent.Type values; consumers
// (UI, dashboard, idle timer) ask for the current state via Current().
//
// Transitions are intentionally lenient: unexpected events don't
// reject — they just keep the current state. Real CLIs sometimes
// reorder events (delta before start, etc.), and crashing on every
// surprise would make agents fragile.
package state

import (
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// State is the per-agent runtime view. See agents-design.md §4.6 step 3.
type State int

const (
	// Idle: subprocess waiting for input, or not yet started.
	Idle State = iota
	// Thinking: parser saw a Thinking event.
	Thinking
	// RunningTool: parser saw a ToolUse event; gate is checking it.
	RunningTool
	// Responding: parser saw TextDelta — text is streaming back.
	Responding
)

// String for log lines / debug.
func (s State) String() string {
	switch s {
	case Thinking:
		return "thinking"
	case RunningTool:
		return "running_tool"
	case Responding:
		return "responding"
	default:
		return "idle"
	}
}

// Lifecycle is the high-level subprocess view that the Backends UI
// renders. Orthogonal to State (substate within an active spawn):
// State answers "what is the agent doing right now"; Lifecycle
// answers "is the subprocess alive, and if so what shape".
//
// Transitions:
//
//	(zero)   → Spawning   when the pool starts a spawn
//	Spawning → Working    on the first event from the CLI
//	Working  → Idle       on Done / Error (substate flips to Idle too)
//	Idle     → Working    on the next event
//	any      → Killed     on subprocess exit (idle TTL, Stop, crash)
//
// The Idle state runs an auto-kill countdown (LastActive + IdleTimeout
// → process killed); the UI renders that as a remaining-time badge.
type Lifecycle int

const (
	// LifecycleSpawning: subprocess started, waiting for first stream
	// event. Renders "loading" in the UI.
	LifecycleSpawning Lifecycle = iota
	// LifecycleWorking: subprocess alive and currently processing a
	// turn. Substate (Thinking / RunningTool / Responding) gives the
	// detail.
	LifecycleWorking
	// LifecycleIdle: subprocess alive but no active turn — auto-kill
	// countdown is running.
	LifecycleIdle
	// LifecycleKilled: subprocess no longer alive. Reason is on the
	// spawn log's exit event.
	LifecycleKilled
)

// String returns the canonical short label used in JSON payloads,
// log lines, and CSS class hooks. Stable across the codebase.
func (l Lifecycle) String() string {
	switch l {
	case LifecycleSpawning:
		return "spawning"
	case LifecycleWorking:
		return "working"
	case LifecycleIdle:
		return "idle"
	case LifecycleKilled:
		return "killed"
	}
	return "unknown"
}

// Machine is goroutine-safe. Apply is called from the parser-reader
// goroutine; Current / LastActive can be read from any goroutine.
type Machine struct {
	mu         sync.RWMutex
	state      State
	lifecycle  Lifecycle
	lastActive time.Time
	now        func() time.Time // injected for tests
}

// New returns a fresh machine in Idle. Caller can inject a clock for
// deterministic tests; pass nil for time.Now.
func New(now func() time.Time) *Machine {
	if now == nil {
		now = time.Now
	}
	return &Machine{state: Idle, lifecycle: LifecycleSpawning, lastActive: now(), now: now}
}

// Lifecycle returns the current high-level lifecycle. Read-only.
func (m *Machine) Lifecycle() Lifecycle {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lifecycle
}

// MarkSpawning resets the machine to the Spawning lifecycle. Pool
// calls this when (re-)spawning a subprocess; idempotent.
func (m *Machine) MarkSpawning() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lifecycle = LifecycleSpawning
	m.lastActive = m.now()
}

// MarkKilled flips the lifecycle to Killed. Pool calls this from the
// OnExit hook regardless of exit reason; the reason is recorded on
// the spawn log's exit event, not here.
func (m *Machine) MarkKilled() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lifecycle = LifecycleKilled
	m.state = Idle
}

// Current returns the current state.
func (m *Machine) Current() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// LastActive returns the timestamp of the most recent event applied.
// The pool's idle timer compares this against IdleTimeoutSec to decide
// when to kill the subprocess.
func (m *Machine) LastActive() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastActive
}

// Apply transitions the machine based on an incoming event. Unknown
// event types are no-ops on state but still bump LastActive — the CLI
// is producing output, which means it's not idle.
//
// Returns the resulting state for callers that want to log transitions.
func (m *Machine) Apply(ev event.AgentEvent) State {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastActive = m.now()

	switch ev.Type {
	case event.Thinking:
		m.state = Thinking
	case event.ToolUse:
		m.state = RunningTool
	case event.TextDelta, event.ToolResult:
		m.state = Responding
	case event.Done, event.Error:
		m.state = Idle
	}
	// Lifecycle: any event from the CLI means the spawn is alive and
	// past the Spawning phase. Done/Error close out the turn → Idle
	// (the auto-kill countdown). Everything else means a turn is
	// in flight → Working.
	if m.lifecycle != LifecycleKilled {
		switch ev.Type {
		case event.Done, event.Error:
			m.lifecycle = LifecycleIdle
		default:
			m.lifecycle = LifecycleWorking
		}
	}
	return m.state
}

// MarkIdle forces the machine back to Idle without touching
// LastActive. Used when the subprocess is killed externally (TTL,
// shutdown) so a stale Responding state doesn't linger in the UI.
func (m *Machine) MarkIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = Idle
}
