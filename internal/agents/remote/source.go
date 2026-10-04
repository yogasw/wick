// Package remote runs agents whose brain lives outside wick. Each source
// (A2A, Slack, …) is an adapter behind one Source interface built on three
// primitives: Send a turn and get a handle, Receive what comes back (push:
// a channel of events; pull: Fetch with a cursor), and Done (a terminal
// event, or idle/timeout decided by the runner). The runner turns either
// style into the stream-json lines the rest of wick already reads.
package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ListenMode is one way a source can hear its replies.
type ListenMode string

const (
	// ListenPush: the source delivers events (stream, socket, callback).
	ListenPush ListenMode = "push"
	// ListenPull: the runner asks with Fetch, backing off 1→2→5→10 s.
	ListenPull ListenMode = "pull"
)

// Turn is one user message sent to the remote.
type Turn struct {
	Text string
	// SessionDir is where the adapter keeps the session's state
	// (A2A contextId, Slack thread_ts, …).
	SessionDir string
	// SessionID is the wick session the turn runs in (see Hops).
	SessionID string
}

// Handle identifies a sent turn: what a reply is matched by (task id,
// thread_ts) and, for pull, the cursor of what was already read.
type Handle struct {
	ID     string
	Cursor string
}

// Limits bound a turn. Max is the hard limit (a turn still running then
// fails); Idle > 0 ends a turn that has shown text and then went quiet for
// that long, labelled NoteNoMarker — never while the remote still shows
// it is working. Poll > 0 caps the wait between two pulls.
//
// TODO: learn per remote (marker use, typical answer time) and adapt Idle.
type Limits struct {
	Max  time.Duration
	Idle time.Duration
	Poll time.Duration
	// Grace > 0 keeps listening that long after a turn ended, for a
	// message or an edit the remote sends late (a Reopener only).
	Grace time.Duration
}

// Reopener is a Source that can keep following a finished turn's thread
// for late messages: Reopen lets the turn's tracker report again.
type Reopener interface {
	Reopen(h Handle)
}

// Description is what Describe shows before an agent is added.
type Description struct {
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
	Target  string `json:"target,omitempty"`
	Kind    string `json:"kind"`
	Warning string `json:"warning,omitempty"`
}

// TestResult is a ping of the remote.
type TestResult struct {
	OK        bool   `json:"ok"`
	State     string `json:"state"`
	LatencyMS int64  `json:"latency_ms"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Source is one remote agent's adapter. Every source also implements
// Pusher, Puller or both, in the order Listen names.
type Source interface {
	// Kind is the adapter key ("a2a", "slack").
	Kind() string
	// Label names the remote for process listings ("a2a-remote (host)").
	Label() string
	// Listen lists the supported ways to hear replies, preferred first.
	Listen() []ListenMode
	Limits() Limits
	// Send delivers turn and returns the handle its reply is matched by.
	Send(ctx context.Context, turn Turn) (Handle, error)
	// Cancel asks the remote to stop h's turn, where it can.
	Cancel(ctx context.Context, h Handle) error
	// End releases what Send set up for h (router entries, timers). It is
	// called once after every turn, however it ended.
	End(h Handle)
	Describe(ctx context.Context) (Description, error)
	Test(ctx context.Context) TestResult
}

// ErrPushUnavailable is returned by Receive when push cannot serve this
// turn (no live event stream); the runner falls back to pull.
var ErrPushUnavailable = errors.New("remote: push not available")

// Pusher delivers h's events on a channel, closed when the source has no
// more to say. A terminal event ends the turn.
type Pusher interface {
	Receive(ctx context.Context, h Handle) (<-chan Event, error)
}

// Puller returns the events since h.Cursor and the handle with the cursor
// moved past them.
type Puller interface {
	Fetch(ctx context.Context, h Handle) ([]Event, Handle, error)
}

// Resumer names the session to the chat (the init line's session_id) from
// the adapter's saved state; without it a fresh id is made per spawn.
type Resumer interface {
	ResumeID(sessionDir string) string
}

// Adapter describes one source kind in the registry.
type Adapter struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Listen is the kind's listen modes, preferred first (plan 6.2c
	// "Cara listen per kanal").
	Listen []ListenMode `json:"listen"`
	// Schema is the Event schema version the adapter emits; it must be
	// SchemaVersion (see event.go).
	Schema int `json:"schema"`
}

var (
	regMu    sync.RWMutex
	registry = map[string]Adapter{}
)

// Register adds an adapter kind; adapters call it from init. An adapter
// built against another Event schema panics at start rather than feed the
// runner events it would misread.
func Register(a Adapter) {
	if a.Schema != SchemaVersion {
		panic(fmt.Sprintf("remote: adapter %q speaks event schema v%d, runner speaks v%d", a.Kind, a.Schema, SchemaVersion))
	}
	regMu.Lock()
	defer regMu.Unlock()
	registry[a.Kind] = a
}

// Lookup returns the adapter of kind.
func Lookup(kind string) (Adapter, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	a, ok := registry[kind]
	return a, ok
}

// Adapters lists the registered kinds, sorted.
func Adapters() []Adapter {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
