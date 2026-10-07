package agents

import (
	"encoding/json"

	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/team"
)

// sessionActivity is the `activity` event of /stream/sessions: what a
// conversation's turn is doing right now, so the Team roster follows it
// from thinking to a tool and back without polling.
//
// Projected, like projectSidebarEvent: only the fields the roster
// renders. The tool's input, its result and the agent's text never ride
// this stream — the action is the same short label the roster shows.
type sessionActivity struct {
	SessionID string `json:"session_id"`
	// Work is "thinking" (a turn with no tool in flight), "tool" (waiting
	// on a tool_use's result) or "" (no turn running).
	Work   string `json:"work"`
	Action string `json:"action"`
	// ToolError: the newest finished tool of the running turn failed and
	// the turn has not started another since (the avatar's error pose).
	ToolError bool `json:"tool_error,omitempty"`
	// NeedsAttention: the conversation waits on a person (ask_user or a
	// tool approval).
	NeedsAttention bool `json:"needs_attention,omitempty"`
}

func (a sessionActivity) JSON() string {
	b, _ := json.Marshal(a)
	return string(b)
}

// activityTracker turns the bus events one /stream/sessions connection
// sees into activity events, sending only what changed.
//
// Only a top-level conversation's own events count. A sub-agent's tool
// calls are not its leader's: the leader's row already shows what the
// leader itself waits on (the delegate call), and the child's liveness
// reaches the parent row through the sub_agent field of the session
// event (projectSidebarEvent). Re-addressing a child's tool here would
// make the parent claim work its own process is not doing — the same
// rule the sidebar keeps. A child's events are therefore dropped, which
// also keeps its session id off the stream.
type activityTracker struct {
	parentOf  func(string) (string, bool)
	visible   func(string) bool
	attention func(string) bool
	last      map[string]sessionActivity
}

func newActivityTracker(parentOf func(string) (string, bool), visible, attention func(string) bool) *activityTracker {
	return &activityTracker{parentOf: parentOf, visible: visible, attention: attention, last: map[string]sessionActivity{}}
}

// root reports whether sessionID is a top-level conversation the caller
// may see.
func (t *activityTracker) root(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	parent, known := t.parentOf(sessionID)
	return known && parent == "" && t.visible(sessionID)
}

// apply folds one bus event in. ok=false when there is nothing to send:
// not an activity event, not a visible top-level conversation, or no
// change since the last event sent for it.
func (t *activityTracker) apply(ev Event) (sessionActivity, bool) {
	switch ev.Type {
	case "tool_use", "tool_result", "lifecycle",
		"ask_user", "ask_user_resolved", "approval_request", "approval_resolved":
	default:
		return sessionActivity{}, false
	}
	if !t.root(ev.SessionID) {
		return sessionActivity{}, false
	}
	prev, seen := t.last[ev.SessionID]
	if !seen {
		prev = sessionActivity{SessionID: ev.SessionID}
	}
	next := prev
	switch ev.Type {
	case "tool_use":
		next.Work, next.Action, next.ToolError = "tool", team.ActionLabel(ev.ToolName, ev.ToolInput), false
	case "tool_result":
		next.Work, next.Action, next.ToolError = "thinking", "", ev.IsError
	case "lifecycle":
		switch ev.Lifecycle {
		case "working", "spawning":
			if next.Work == "" {
				next.Work = "thinking"
			}
		default:
			// The turn is over: whatever tool it named is done, and a
			// failure belongs to that turn only.
			next.Work, next.Action, next.ToolError = "", "", false
		}
	default:
		next.NeedsAttention = t.attention(ev.SessionID)
	}
	if next == prev {
		return sessionActivity{}, false
	}
	t.remember(next)
	return next, true
}

// replay is the activity of every visible conversation with a turn in
// flight, for a freshly connected subscriber. A turn waiting on a person
// is in flight too (ask_user and approvals block it), so the pool's
// snapshot covers needs_attention as well.
func (t *activityTracker) replay(entries []pool.ActiveEntry) []sessionActivity {
	var out []sessionActivity
	for _, e := range entries {
		if e.Lifecycle != "working" && e.Lifecycle != "spawning" || !t.root(e.SessionID) {
			continue
		}
		a := sessionActivity{SessionID: e.SessionID, Work: "thinking", NeedsAttention: t.attention(e.SessionID)}
		if action := team.CurrentAction(e.InFlightEvents); action != "" {
			a.Work, a.Action = "tool", action
		} else {
			a.ToolError = team.ToolFailed(e.InFlightEvents)
		}
		t.remember(a)
		out = append(out, a)
	}
	return out
}

// remember keeps a.SessionID's last sent state; an idle conversation
// nobody waits on is forgotten, so the map only holds live ones.
func (t *activityTracker) remember(a sessionActivity) {
	if a.Work == "" && !a.NeedsAttention {
		delete(t.last, a.SessionID)
		return
	}
	t.last[a.SessionID] = a
}

// sessionNeedsAttention reports whether sessionID waits on a person:
// an open ask_user question or a pending tool approval.
func sessionNeedsAttention(sessionID string) bool {
	if globalAskUsers != nil && len(globalAskUsers.PendingFor(sessionID)) > 0 {
		return true
	}
	if globalApprovals != nil {
		for _, r := range globalApprovals.PendingFor("") {
			if r.SessionID == sessionID {
				return true
			}
		}
	}
	return false
}
