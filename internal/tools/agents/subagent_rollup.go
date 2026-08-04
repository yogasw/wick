package agents

// rollUpSubAgentWork attributes each live sub-agent's lifecycle to the
// top-level conversation that owns it.
//
// A sub-agent runs under its own session id, and that id is deliberately
// absent from the sidebar and from /stream/sessions — a child is not a
// conversation. The consequence was that a session whose own process had
// gone idle looked completely dormant while its sub-agent was still
// working, which is precisely the stretch where the user most wants to
// know something is running.
//
// parentOf reports the parent session id of a child and whether the
// session is known at all; a top-level session answers ("", true). Only
// "working" and "spawning" count: an idle child holds a live process
// without doing anything, and treating that as busy would light the row
// up for as long as the process lingered.
//
// Returns rootSessionID → busiest child lifecycle. "working" outranks
// "spawning" so one busy child is never masked by a quieter sibling,
// regardless of map iteration order.
func rollUpSubAgentWork(live map[string]string, parentOf func(string) (string, bool)) map[string]string {
	out := make(map[string]string)
	for sessionID, lifecycle := range live {
		if lifecycle != "working" && lifecycle != "spawning" {
			continue
		}
		root, ok := rootOf(sessionID, parentOf)
		// Not a sub-agent (it IS a root), unknown to the registry, or in a
		// metadata cycle — none of which has a sidebar row to light up.
		if !ok || root == "" || root == sessionID {
			continue
		}
		if out[root] != "working" {
			out[root] = lifecycle
		}
	}
	return out
}

// projectSidebarEvent turns one lifecycle transition into the event the
// sidebar should receive, addressed to a row that actually exists.
//
// A leader's transition passes through as-is. A sub-agent's is
// re-addressed to the conversation at the top of its chain and moved onto
// the SubAgent field, so the row can show delegated work without ever
// claiming the leader's own process is busy.
//
// A child that stops (idle / killed / anything but working or spawning)
// still produces an event, with SubAgent empty — that emptiness is what
// clears the parent's marker. Without it the teal spinner would outlive
// the work it describes.
//
// ok=false means there is nothing to send: the session is unknown, so
// there is no row to address.
func projectSidebarEvent(sessionID, lifecycle string, parentOf func(string) (string, bool)) (Event, bool) {
	parent, known := parentOf(sessionID)
	if !known {
		return Event{}, false
	}
	if parent == "" {
		return Event{SessionID: sessionID, Type: "lifecycle", Lifecycle: lifecycle}, true
	}
	root, ok := rootOf(sessionID, parentOf)
	if !ok || root == "" {
		return Event{}, false
	}
	sub := ""
	if lifecycle == "working" || lifecycle == "spawning" {
		sub = lifecycle
	}
	return Event{SessionID: root, Type: "lifecycle", SubAgent: sub}, true
}

// sessionParentOf is the registry-backed parentOf for rollUpSubAgentWork.
//
// Distinct from parentOfChildSession, which collapses "unknown session"
// and "top-level session" into the same "" answer. The roll-up needs them
// apart: a top-level session terminates the climb, while a session the
// registry has never heard of must be dropped rather than silently
// treated as a root.
func sessionParentOf(sessionID string) (string, bool) {
	if globalMgr == nil {
		return "", false
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return "", false
	}
	return sess.Meta.ParentSessionID, true
}

// rootOf climbs the parent chain to the top-level session. Sub-agents
// nest, so the first parent is not necessarily the one with a sidebar
// row.
//
// The hop budget bounds a cycle from corrupt metadata: a render path must
// not hang, and no legitimate delegation tree approaches this depth.
func rootOf(sessionID string, parentOf func(string) (string, bool)) (string, bool) {
	const maxHops = 32
	cur := sessionID
	for range maxHops {
		parent, known := parentOf(cur)
		if !known {
			return "", false
		}
		if parent == "" {
			return cur, true
		}
		cur = parent
	}
	return "", false
}
