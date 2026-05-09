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

// Machine is goroutine-safe. Apply is called from the parser-reader
// goroutine; Current / LastActive can be read from any goroutine.
type Machine struct {
	mu         sync.RWMutex
	state      State
	lastActive time.Time
	now        func() time.Time // injected for tests
}

// New returns a fresh machine in Idle. Caller can inject a clock for
// deterministic tests; pass nil for time.Now.
func New(now func() time.Time) *Machine {
	if now == nil {
		now = time.Now
	}
	return &Machine{state: Idle, lastActive: now(), now: now}
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
