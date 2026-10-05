package agents

import "encoding/json"

// evSubAgent is the signal a conversation's own stream gets when one of
// its sub-agents changed: started or stopped working, or finished a turn
// (its turn count moved). The chat's Sub-agents panel refetches its rows
// on it instead of polling while a sub-agent runs.
//
// A sub-agent publishes on its OWN session id, which the leader's chat
// is not subscribed to; without this signal the panel had no way to see
// the child move between the delegation call and the leader's end of turn.
const evSubAgent = "sub_agent"

// subAgentSignalData is the whole payload: which child and what kind of
// change. Rows themselves stay REST (the sub-agent panel endpoint), so
// nothing the child said or ran rides this signal.
type subAgentSignalData struct {
	ChildSessionID string `json:"child_session_id"`
	// State is the child's lifecycle ("working", "idle", …) or "turn"
	// when a turn ended.
	State string `json:"state"`
}

// subAgentSignal derives the sub_agent signal for an event published on
// sessionID, addressed to its DIRECT parent only — the conversation whose
// panel lists this child. It goes to that session's subscribers and never
// to the global key, so it reaches exactly the viewers who already passed
// the access check for the parent (ownsSession on /stream and
// /stream/multi), and the parent's panel already names the child.
func (b *Broadcaster) subAgentSignal(sessionID string, ev Event) (string, Event, bool) {
	if b.parentOf == nil || sessionID == "" {
		return "", Event{}, false
	}
	var state string
	switch ev.Type {
	case "lifecycle":
		state = ev.Lifecycle
	case "done":
		state = "turn"
	default:
		return "", Event{}, false
	}
	parent, ok := b.parentOf(sessionID)
	if !ok || parent == "" || parent == sessionID {
		return "", Event{}, false
	}
	body, _ := json.Marshal(subAgentSignalData{ChildSessionID: sessionID, State: state})
	return parent, Event{SessionID: parent, Type: evSubAgent, Data: string(body)}, true
}
