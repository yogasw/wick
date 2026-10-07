package agents

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/remote/pluginremote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// A remote agent used to carry its own "Who may use it" setting (usage
// only_me / me_and_my_agents) next to the mention policy every agent has.
// The two meant nearly the same thing, so the mention policy is now the
// only one: usage "mention" marks a remote agent whose MentionFrom
// decides, and migrateRemoteUsage carries an old value over once.

// remoteUsageByMention is the shared value of a2aremote, slackremote and
// pluginremote UsageByMention.
const remoteUsageByMention = a2aremote.UsageByMention

// remoteUsage is remote agent p's stored usage, with the old default
// applied: a plugin row saved before the merge took no agent's turn.
// ok=false when its settings are missing or unreadable.
func remoteUsage(p entity.AgentPersona) (string, bool) {
	switch {
	case isSlackRemote(p):
		if slackRemoteStore() == nil {
			return "", false
		}
		cfg, ok, err := slackRemoteStore().Load(p.ID)
		if err != nil || !ok {
			return "", false
		}
		if cfg.Usage == slackremote.UsageByMention {
			return remoteUsageByMention, true
		}
		return cfg.EffectiveUsage(), true
	case isPluginRemote(p):
		if pluginRemoteStore() == nil {
			return "", false
		}
		cfg, ok, err := pluginRemoteStore().Load(p.ID)
		if err != nil || !ok {
			return "", false
		}
		if cfg.Usage == pluginremote.UsageByMention {
			return remoteUsageByMention, true
		}
		return a2aremote.UsageOnlyMe, true
	case isA2ARemote(p):
		if remoteStore() == nil {
			return "", false
		}
		cfg, ok, err := remoteStore().Load(p.ID)
		if err != nil || !ok {
			return "", false
		}
		if cfg.Usage == a2aremote.UsageByMention {
			return remoteUsageByMention, true
		}
		return cfg.EffectiveUsage(), true
	}
	return "", false
}

// remoteMentionDefault is a new remote agent's mention policy: "Nobody"
// (owner only), unless an older client still sent usage
// me_and_my_agents.
func remoteMentionDefault(usage string) string {
	if usage == a2aremote.UsageMeAndAgents {
		return teamlink.MentionAll
	}
	return teamlink.MentionOff
}

// markRemoteByMention stores usage "mention" on remote agent p's settings.
func markRemoteByMention(p entity.AgentPersona) error {
	switch {
	case isSlackRemote(p):
		cfg, ok, err := slackRemoteStore().Load(p.ID)
		if err != nil || !ok {
			return err
		}
		cfg.Usage = slackremote.UsageByMention
		return slackRemoteStore().Save(cfg)
	case isPluginRemote(p):
		cfg, ok, err := pluginRemoteStore().Load(p.ID)
		if err != nil || !ok {
			return err
		}
		cfg.Usage = pluginremote.UsageByMention
		return pluginRemoteStore().Update(cfg)
	case isA2ARemote(p):
		cfg, ok, err := remoteStore().Load(p.ID)
		if err != nil || !ok {
			return err
		}
		cfg.Usage = a2aremote.UsageByMention
		return remoteStore().Save(cfg)
	}
	return nil
}

// migrateRemoteUsage carries every remote agent's old usage into its
// mention policy: only_me becomes MentionOff ("Nobody" — the owner can
// still chat, agents are refused), me_and_my_agents keeps the policy it
// has. Idempotent: a migrated agent is marked usage "mention" and skipped.
func migrateRemoteUsage(ctx context.Context) {
	if globalDB == nil || globalTeam == nil {
		return
	}
	rows, err := globalTeam.ListKinds(ctx, a2aremote.Kind, slackremote.Kind, pluginremote.Kind)
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("team: list remote agents for usage migration")
		return
	}
	for i := range rows {
		if err := migrateOneRemoteUsage(ctx, &rows[i]); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("agent", rows[i].ID).Msg("team: migrate remote agent usage")
		}
	}
}

// migrateOneRemoteUsage is migrateRemoteUsage for one agent.
func migrateOneRemoteUsage(ctx context.Context, p *entity.AgentPersona) error {
	usage, ok := remoteUsage(*p)
	if !ok || usage == remoteUsageByMention {
		return nil
	}
	if usage != a2aremote.UsageMeAndAgents && p.MentionFrom != teamlink.MentionOff {
		p.MentionFrom = teamlink.MentionOff
		if err := globalTeam.Update(ctx, p); err != nil {
			return err
		}
	}
	return markRemoteByMention(*p)
}
