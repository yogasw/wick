package agents

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/yogasw/wick/internal/entity"
)

// evAgentChanged is the bus signal that a Team roster may be stale: an
// agent was created, edited, deleted or made Captain, its shares or share
// tags changed, or a group chat changed. /stream/sessions forwards it as
// an `agent_changed` event, and the Team app refetches its roster on it
// instead of polling every 30 seconds.
const evAgentChanged = "agent_changed"

// agentSignal is what a viewer receives: which agent (or group) changed
// and nothing else. The roster itself stays REST, which applies its own
// access rule again.
type agentSignal struct {
	AgentID string `json:"agent_id,omitempty"`
	GroupID string `json:"group_id,omitempty"`
}

// agentSignalBus is the bus payload: the signal plus who may receive it.
// The audience is decided at write time, against the people with a
// /stream/sessions open — before the write (who saw the agent) and after
// it (who sees it now) — so an unshare or a delete still reaches the
// roster it disappears from. It never leaves the server.
type agentSignalBus struct {
	agentSignal
	Audience []string `json:"audience"`
}

// streamViewers counts the open /stream/sessions connections per user:
// the only people an agent_changed signal can reach, so the only ones an
// audience is worked out for.
var streamViewers = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// addStreamViewer registers uid's connection; call the result when it
// closes.
func addStreamViewer(uid string) func() {
	if uid == "" {
		return func() {}
	}
	streamViewers.Lock()
	streamViewers.n[uid]++
	streamViewers.Unlock()
	return func() {
		streamViewers.Lock()
		if streamViewers.n[uid]--; streamViewers.n[uid] <= 0 {
			delete(streamViewers.n, uid)
		}
		streamViewers.Unlock()
	}
}

func streamViewerIDs() []string {
	streamViewers.Lock()
	defer streamViewers.Unlock()
	ids := make([]string, 0, len(streamViewers.n))
	for id := range streamViewers.n {
		ids = append(ids, id)
	}
	return ids
}

// teamAgentVisibleTo is the roster's own rule for one agent: the owner's
// list (team.Store.List) plus what is shared with them (sharedWithUser,
// the per-agent form of sharedAgentsFor that the chat routes use).
func teamAgentVisibleTo(ctx context.Context, p entity.AgentPersona, uid string) bool {
	if uid == "" {
		return false
	}
	if p.OwnerUserID == uid {
		return true
	}
	_, ok := sharedWithUser(ctx, p, uid)
	return ok
}

// agentAudience is who, among the connected viewers, sees agentID now. A
// missing agent is seen by nobody.
func agentAudience(ctx context.Context, agentID string) []string {
	if globalTeam == nil {
		return nil
	}
	viewers := streamViewerIDs()
	if len(viewers) == 0 {
		return nil
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil {
		return nil
	}
	var out []string
	for _, uid := range viewers {
		if teamAgentVisibleTo(ctx, p, uid) {
			out = append(out, uid)
		}
	}
	return out
}

// agentChangeHook is the team.Store change hook: who sees the agent before
// the write, then after it, and one signal to both.
func agentChangeHook(ctx context.Context, agentID string) func() {
	before := agentAudience(ctx, agentID)
	return func() {
		audience := append(before, agentAudience(ctx, agentID)...)
		publishAgentSignal(globalBcast, agentSignal{AgentID: agentID}, audience)
	}
}

// TrackTeamAgentChange is agentChangeHook for writes made outside the team
// store — the admin page's share tags. Call the result once the write is
// saved.
func TrackTeamAgentChange(ctx context.Context, agentID string) func() {
	if agentID == "" {
		return func() {}
	}
	return agentChangeHook(ctx, agentID)
}

// publishGroupChanged signals the group's owner — the only person whose
// roster lists it (apiTeamGroupList).
func publishGroupChanged(groupID, ownerID string) {
	publishAgentSignal(globalBcast, agentSignal{GroupID: groupID}, []string{ownerID})
}

// publishAgentSignal publishes sig for audience on the global key. Nobody
// to tell, nothing published.
func publishAgentSignal(b *Broadcaster, sig agentSignal, audience []string) {
	slices.Sort(audience)
	audience = slices.Compact(audience)
	audience = slices.DeleteFunc(audience, func(s string) bool { return s == "" })
	if b == nil || len(audience) == 0 || (sig.AgentID == "" && sig.GroupID == "") {
		return
	}
	body, _ := json.Marshal(agentSignalBus{agentSignal: sig, Audience: audience})
	b.PublishRaw("", "", evAgentChanged, string(body))
}

// projectAgentSignal re-reads a bus signal for one /stream/sessions
// caller: ok only when uid is in its audience, and the audience itself is
// dropped from what is sent.
func projectAgentSignal(ev Event, uid string) (string, bool) {
	var s agentSignalBus
	if uid == "" || json.Unmarshal([]byte(ev.Data), &s) != nil || !slices.Contains(s.Audience, uid) {
		return "", false
	}
	b, _ := json.Marshal(s.agentSignal)
	return string(b), true
}
