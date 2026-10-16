package agents

import (
	"sync"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

// A conversation waiting on teammates (working Team tasks it sent) lights
// its sidebar row the way a working sub-agent does: the teammate's chat is
// not a child of it, so the parent-chain roll-up never sees that work.

// evTeamWaiting is the bus signal that a session's "waiting on teammates"
// may have changed. Data is the SubAgent marker its row should show now.
const evTeamWaiting = "team_waiting"

// teamWaitingSessions is every session with a working task it sent.
func teamWaitingSessions(live []teamlink.LiveTask) map[string]bool {
	out := map[string]bool{}
	for _, t := range live {
		if t.Working && t.FromSession != "" {
			out[t.FromSession] = true
		}
	}
	return out
}

// liveTeamTasks reads the Hub's open tasks; nil without Team.
func liveTeamTasks() []teamlink.LiveTask {
	if globalTeamHub == nil {
		return nil
	}
	if h := globalTeamHub(); h != nil {
		return h.LiveTasks()
	}
	return nil
}

// teamWaiting reports whether sessionID waits on a working teammate.
func teamWaiting(sessionID string) bool {
	return teamWaitingSessions(liveTeamTasks())[sessionID]
}

// sidebarWorkMarker is the SubAgent marker for a row: "working" while a
// teammate or a sub-agent of it works, else childMarker (a sub-agent's own
// lifecycle, "" when none). Either source finishing must not clear the
// other's still-live work.
func sidebarWorkMarker(waiting bool, childMarker string) string {
	if waiting {
		return "working"
	}
	return childMarker
}

// childWork is the busiest lifecycle of sessionID's live sub-agents.
func childWork(sessionID string) string {
	if globalPool == nil {
		return ""
	}
	live := make(map[string]string)
	for _, e := range globalPool.ActiveSnapshot() {
		live[e.SessionID] = e.Lifecycle
	}
	return rollUpSubAgentWork(live, sessionParentOf)[sessionID]
}

// teamWaitingLocks serialises publishTeamWaiting per session: the state is
// read and published under the same lock, so a stale "working" read can
// never land after the "" of a later change.
var teamWaitingLocks sync.Map // session → *sync.Mutex

// publishTeamWaiting tells the sidebar streams what sessionID's row shows
// after one of its tasks moved.
func publishTeamWaiting(b *Broadcaster, live func() []teamlink.LiveTask, sessionID string) {
	if b == nil || live == nil || sessionID == "" {
		return
	}
	mu, _ := teamWaitingLocks.LoadOrStore(sessionID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	waiting := teamWaitingSessions(live())[sessionID]
	b.PublishRaw(sessionID, "", evTeamWaiting, sidebarWorkMarker(waiting, childWork(sessionID)))
}
