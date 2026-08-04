package channels

import (
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
)

// Sub-agent progress relay.
//
// A sub-agent runs under its own session ID (`<parent>--sub-<seed>`), so its
// events arrive at DispatchAgentEvent addressed to a session no channel has a
// thread for — every channel drops them. Meanwhile the leader that delegated
// is blocked waiting and emits nothing at all. The result on a channel is a
// status banner frozen on "Delegating: …" for as long as the child runs, which
// reads as a hung agent rather than a working one.
//
// The relay closes that gap: a child's STATUS events are re-addressed to the
// nearest ancestor session a channel actually owns, tagged with the child's
// label, so the leader's thread keeps showing live progress ("researcher →
// Reading store.go") while the child works.
//
// Only status events are relayed — see relayableToAncestor. The child's reply
// is NOT: it reaches the conversation as the delegation's result (see
// poolDeliverer.DeliverToChannel), and relaying the text as well would post
// the same answer to the thread twice.

// relayableToAncestor reports whether a child event carries progress worth
// showing on the leader's thread.
//
// ToolUse/ToolResult/Thinking say "the child is moving, here is what it is
// doing" — exactly what a frozen banner is missing. TextDelta and Done are
// excluded because the delegation result path already delivers the child's
// output and its completion; relaying either would duplicate the reply or end
// the leader's turn early, while the leader is still blocked.
//
// Error is excluded for the same reason: a failed child is reported through
// the delegation result, which can explain the failure. Relaying it here would
// terminate the leader's in-flight turn on the channel while the leader itself
// is still running.
func relayableToAncestor(t event.EventType) bool {
	switch t {
	case event.ToolUse, event.ToolResult, event.Thinking:
		return true
	default:
		return false
	}
}

// subAgentLabel derives a short, human-meaningful name for the child from its
// session ID.
//
// The last `--sub-<seed>` segment is a delegation-UUID prefix, which tells a
// reader nothing. A registered agent name is preferred when the caller has one
// (the pool passes it); this is the fallback so the relay still attributes
// work rather than showing it unlabelled.
func subAgentLabel(childSessionID, agentName string) string {
	if agentName != "" {
		return agentName
	}
	seed := childSessionID
	if i := strings.LastIndex(childSessionID, agentconfig.SubSessionSep); i >= 0 {
		seed = childSessionID[i+len(agentconfig.SubSessionSep):]
	}
	if seed == "" {
		return "sub-agent"
	}
	return "sub-agent " + seed
}

// ancestorForRelay walks up the `--sub-` chain and returns the nearest
// ancestor session that some channel is currently rendering a turn for, or ""
// when no ancestor has one (a delegation started from the web UI or an API
// call — there is no banner to keep alive, and the web UI already shows the
// child directly through its own SSE stream).
//
// Nearest-first matters for nested delegation: a grandchild's progress belongs
// on the thread of whichever ancestor is actually on a channel, and walking up
// from the bottom finds it without assuming the tree is only one level deep.
//
// "Has a live turn" is the ownership test rather than a name/prefix match
// because it is precisely the condition that makes a relay useful: a channel
// with an in-flight turn has a status banner to update, and one without it
// would drop the event anyway. It also keeps the relay transport-agnostic —
// no channel needs to expose how it derives session IDs.
func (r *Registry) ancestorForRelay(childSessionID string) string {
	for id := agentconfig.ParentOfSubSession(childSessionID); id != ""; id = agentconfig.ParentOfSubSession(id) {
		if r.anyChannelHasTurn(id) {
			return id
		}
	}
	return ""
}

// anyChannelHasTurn reports whether a registered channel has an in-flight turn
// for sessionID.
//
// Channels that don't implement LiveTurnReporter are skipped rather than
// assumed to have one: a wrong "yes" here would stop the walk at an ancestor
// nobody is rendering, silently dropping progress that a higher ancestor could
// have shown.
func (r *Registry) anyChannelHasTurn(sessionID string) bool {
	for _, c := range r.Channels() {
		if x, ok := c.(LiveTurnReporter); ok && x.HasLiveTurn(sessionID) {
			return true
		}
	}
	return false
}

// relayFromSubAgent forwards one child status event onto its nearest
// channel-owning ancestor's thread, tagged with the child's label. Reports
// whether anything was relayed.
//
// It bypasses the silent-turn buffer deliberately. That buffer decides whether
// a REPLY is silent by inspecting its opening text, and a relayed status event
// carries no reply text — routing these through it would hold a child's
// progress hostage to a decision that can only be made from the leader's own
// output, which is exactly the output that is not arriving while the leader
// waits. The leader's own turn state is untouched, so a leader that does open
// with [silent] still has its reply suppressed on the normal path.
func (r *Registry) relayFromSubAgent(childSessionID, agentName string, ev event.AgentEvent) bool {
	if !relayableToAncestor(ev.Type) {
		return false
	}
	ancestor := r.ancestorForRelay(childSessionID)
	if ancestor == "" {
		return false
	}
	ev.SubAgent = subAgentLabel(childSessionID, agentName)
	r.forward(ancestor, []event.AgentEvent{ev})
	return true
}
