package agents

import (
	"slices"
	"strings"

	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// A Slack destination is no new column either: creating it posts
// "⏰ Scheduled …" into the channel through the agent's Slack connection
// and the schedule fires into that message's thread session. The session
// carries the thread binding, so the Slack instance restores the turn on
// every fire and answers in the thread although nobody typed there
// (agentslack.Channel.ensureTurn).

// slackChannelVM is one channel the drawer offers for a Slack destination.
type slackChannelVM struct {
	ID string `json:"id"`
}

// agentSlackTarget is the Slack instance p's schedules post through.
type agentSlackTarget struct {
	ch *agentslack.Channel
	// as is the Instant persona the thread opens under (zero for Custom).
	as agentslack.Persona
	// mode is "custom" (the agent's own app) or "instant" (the shared app).
	mode  string
	bound []string
}

// agentSlackScheduleTarget reports whether p can post to Slack now: its own
// bot connected and online, or Instant mode on a running shared app.
func agentSlackScheduleTarget(p entity.AgentPersona) (agentSlackTarget, bool) {
	if globalDB == nil || globalChannels == nil || p.Disabled {
		return agentSlackTarget{}, false
	}
	if st, ok := agentSlackOf(p); ok && st.Connected && st.Online && !st.Disabled {
		ch, _ := globalChannels.ChannelByKey(agentSlackInstanceKey(p.ID)).(*agentslack.Channel)
		if ch != nil && ch.IsConfigured() {
			return agentSlackTarget{ch: ch, mode: "custom"}, true
		}
	}
	row, cfg, found, err := instantRow(globalDB, p.ID)
	if err != nil || !found || !row.Enabled {
		return agentSlackTarget{}, false
	}
	ch := sharedSlackInstance(cfg.SharedChannel)
	if ch == nil || !ch.IsConfigured() {
		return agentSlackTarget{}, false
	}
	t := agentSlackTarget{ch: ch, mode: "instant", bound: cfg.BoundChannels}
	for _, a := range instantCache.get().byInstance[cfg.SharedChannel] {
		if a.AgentID == p.ID {
			t.as = a.Persona
		}
	}
	return t, true
}

// agentSlackChannels are the channels the drawer lists: an Instant agent's
// bound channels plus every channel a thread of the agent already lives
// in. A channel never seen still works by pasting its id or link.
func agentSlackChannels(p entity.AgentPersona, t agentSlackTarget) []slackChannelVM {
	seen := map[string]bool{}
	var out []slackChannelVM
	add := func(id string) {
		if id, ok := normalizeSlackChannel(id); ok && !seen[id] {
			seen[id] = true
			out = append(out, slackChannelVM{ID: id})
		}
	}
	for _, id := range t.bound {
		add(id)
	}
	if globalMgr != nil {
		prefix := agentSlackSessionPrefix(p.ID)
		for _, s := range globalMgr.Registry().Sessions() {
			if s.Meta.AgentID != p.ID && !strings.HasPrefix(s.ID, prefix) {
				continue
			}
			if r := s.Meta.ChannelRef; r != nil && r.Channel == "slack" {
				add(r.ChatID)
			}
		}
	}
	slices.SortFunc(out, func(a, b slackChannelVM) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// scheduleSlackChannelOf is the Slack channel sessionID — one of agentID's
// sessions — is a thread in, "" when it is not a Slack thread.
func scheduleSlackChannelOf(sessionID, agentID string) string {
	if globalMgr == nil || sessionID == "" {
		return ""
	}
	s, ok := globalMgr.Registry().Session(sessionID)
	if !ok || (s.Meta.AgentID != agentID && !strings.HasPrefix(sessionID, agentSlackSessionPrefix(agentID))) {
		return ""
	}
	if r := s.Meta.ChannelRef; r != nil && r.Channel == "slack" {
		return r.ChatID
	}
	return ""
}

// openSlackScheduleThread posts the schedule's anchor into channel and
// creates the agent session of its thread, bound to it. The bot must be in
// the channel; Slack's refusal comes back as a 400 the drawer shows.
func openSlackScheduleThread(c *tool.Ctx, p entity.AgentPersona, channel, message string) (string, error) {
	t, ok := agentSlackScheduleTarget(p)
	if !ok {
		return "", errScheduleDest{"the agent has no active Slack connection"}
	}
	id, ok := normalizeSlackChannel(channel)
	if !ok {
		return "", errScheduleDest{"pick a Slack channel, or paste its id (C0123ABCD) or link"}
	}
	title := scheduledTitle(message)
	key, binding, err := t.ch.OpenThread(id, "⏰ Scheduled “"+title+"” — each run is answered in this thread.", t.as)
	if err != nil {
		return "", errScheduleDest{"cannot post to Slack channel " + id + " (" + err.Error() + ") — invite the bot to the channel first"}
	}
	if _, err := createTeamAgentSessionID(c, p, false, key, session.OriginSlack); err != nil {
		return "", err
	}
	sess, err := session.Load(globalLayout, key)
	if err != nil {
		return "", err
	}
	sess.Meta.ChannelRef = &session.ChannelRef{
		Channel: binding.Channel, ChatID: binding.ChatID, ThreadID: binding.ThreadID, Instance: binding.Instance,
	}
	if err := session.SaveMeta(globalLayout, key, sess.Meta); err != nil {
		return "", err
	}
	_ = globalMgr.RefreshSession(key)
	return key, nil
}
