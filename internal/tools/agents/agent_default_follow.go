package agents

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

// A Team agent's chats are made on its project's default provider and
// model, then keep whatever agents.json names. When the default changes,
// they follow it by these rules:
//
//   - The agent keeps its provider (allow_provider_switch off): nobody
//     could pick another one, so every chat follows the default. Chats
//     already out of step (made before the change, or before this rule)
//     are put right lazily too, at their next message (agentDefaultFor).
//   - The agent lets chats switch: a chat still on the OLD default was
//     following it and moves; one on another provider was picked by hand,
//     and the pick wins.
//   - The agent forces its default (Settings "Keep every chat on the
//     default provider", team.SessionPolicy.ForceDefaultProvider): every
//     chat follows the default as when the agent keeps its provider, hand
//     picks included, except a chat running on a failover stand-in — the
//     failover and its return home own it. Turning it on sweeps once.
//
// No turn is cut short: the switch is only persisted to agents.json here
// (ApplySwitch), and the pool stops the idle process at the chat's next
// message and respawns it on the new provider (maybeFollowDefault). A
// turn in flight finishes on the provider it started on.
func init() { provider.AgentDefault = agentDefaultFor }

// agentDefaultFor is provider.AgentDefault: a chat of an agent that keeps
// its provider, or forces its default, runs on the agent's default; any
// other chat picks its own.
func agentDefaultFor(ctx context.Context, sessionID string) (string, string, bool) {
	if globalMgr == nil || globalTeam == nil {
		return "", "", false
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok || sess.Meta.AgentID == "" {
		return "", "", false
	}
	p, err := globalTeam.Get(ctx, sess.Meta.AgentID)
	if err != nil {
		return "", "", false
	}
	if team.ForcesDefaultProvider(p.SessionPolicy) {
		// agents.json from disk: the registry's copy can trail a failover.
		if globalLayout != (agentconfig.Layout{}) {
			if fresh, err := session.Load(globalLayout, sessionID); err == nil {
				sess = fresh
			}
		}
		if _, a, ok := sessionAgentEntry(sess); ok && onFailover(a) {
			return "", "", false
		}
	} else if team.AllowsProviderSwitch(p.AllowProviderSwitch, p.IsCaptain) {
		return "", "", false
	}
	prov, model := agentDefaultOf(p)
	return prov, model, prov != ""
}

// agentDefaultOf is agent p's default provider ("type/name") and model,
// from its project; "" when it has none (the server default, nothing to
// follow) or is a remote agent.
func agentDefaultOf(p entity.AgentPersona) (prov, model string) {
	if p.ProjectID == "" || IsRemoteAgent(p) {
		return "", ""
	}
	proj, ok := globalMgr.Registry().Project(p.ProjectID)
	if !ok {
		return "", ""
	}
	prov = strings.TrimSpace(proj.Meta.Defaults.Provider)
	if prov == "" {
		return "", ""
	}
	return normalizeProviderKey(prov), strings.TrimSpace(proj.Meta.Defaults.Model)
}

// followAgentDefault moves agent p's existing chats onto its current
// default, by the rules above. oldProv is the default before the change
// (the same as the current one on a re-save: then only chats of an agent
// that keeps its provider can move). Returns how many chats moved.
func followAgentDefault(ctx context.Context, p entity.AgentPersona, oldProv string) int {
	if globalMgr == nil || globalLayout == (agentconfig.Layout{}) {
		return 0
	}
	prov, model := agentDefaultOf(p)
	if prov == "" {
		return 0
	}
	if oldProv = strings.TrimSpace(oldProv); oldProv != "" {
		oldProv = normalizeProviderKey(oldProv)
	}
	forced := team.ForcesDefaultProvider(p.SessionPolicy)
	handPicks := !forced && team.AllowsProviderSwitch(p.AllowProviderSwitch, p.IsCaptain)
	moved := 0
	for id, s := range globalMgr.Registry().Sessions() {
		if s.Meta.AgentID != p.ID {
			continue
		}
		// agents.json from disk: the registry's copy can trail a switch.
		fresh, err := session.Load(globalLayout, id)
		if err != nil {
			continue
		}
		name, a, ok := sessionAgentEntry(fresh)
		if !ok {
			continue
		}
		cur := normalizeProviderKey(a.Provider)
		home, homeModel := cur, a.ModelID
		if a.Failover != nil && a.Failover.HomeProvider != "" {
			home, homeModel = normalizeProviderKey(a.Failover.HomeProvider), a.Failover.HomeModelID
		}
		switch {
		case forced && onFailover(a):
			continue // on a stand-in: the failover owns it
		case home == prov && homeModel == model:
			continue // already there
		case cur == prov && a.ModelID == model:
			continue // a stand-in that is the new default: nothing to switch
		case handPicks && home != oldProv:
			continue // picked by hand: the pick wins
		}
		bcast := globalBcast
		if _, err := provider.ApplySwitch(globalLayout, id, name, prov, provider.SwitchOptions{
			Source:     "agent",
			ModelID:    model,
			ExpectFrom: a.Provider,
			Notify: func(tag string, steps []string) {
				if bcast != nil {
					bcast.PublishSystemTurn(id, name, provider.SwitchChipText(tag, model), steps)
				}
			},
		}); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("session", id).Str("agent", p.ID).Str("to", prov).Msg("team: chat could not follow the agent's new default provider")
			continue
		}
		_ = globalMgr.RefreshSession(id)
		moved++
	}
	return moved
}

// onFailover reports whether a runs on a failover stand-in, or is in the
// middle of a failover run, rather than on its home.
func onFailover(a session.AgentEntry) bool {
	st := a.Failover
	if st == nil {
		return false
	}
	if st.AwayFromHome() {
		return true
	}
	return st.HomeProvider != "" && a.Provider != "" && normalizeProviderKey(st.HomeProvider) != normalizeProviderKey(a.Provider)
}

// sessionAgentEntry is the agents.json entry s talks to: its active
// agent, else its first.
func sessionAgentEntry(s session.Session) (string, session.AgentEntry, bool) {
	name := s.Meta.ActiveAgent
	for _, a := range s.Agents {
		if name == "" || a.Name == name {
			return a.Name, a, true
		}
	}
	return "", session.AgentEntry{}, false
}
