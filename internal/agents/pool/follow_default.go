package pool

import (
	"context"
	"errors"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
)

// maybeFollowDefault puts sessionID's agent on the provider it should run
// on, at a turn boundary: right before a new user message goes out, while
// nothing runs. Two cases:
//
//   - provider.AgentDefault names a default the agent's home is not on
//     (its Team agent's Settings fix the provider, and the default was
//     changed after the session was made): a manual switch there, so the
//     default becomes the home.
//   - the live process runs another instance or model pin than
//     agents.json names (a switch was persisted while a turn was in
//     flight): the process is stopped, the message respawns it on what
//     agents.json names.
//
// Never cuts a turn short, as maybeReturnHome: a busy agent, a spawn in
// flight or a failover skip it, and the stop is Agent.StopIfIdle. A
// failover stand-in is left alone while its home is the default (the
// return home is maybeReturnHome's). true = it switched or stopped.
func (p *Pool) maybeFollowDefault(ctx context.Context, sessionID, agentName string) bool {
	key := sessionKey(sessionID, agentName)
	if p.FailingOver(sessionID, agentName) {
		return false
	}
	p.mu.Lock()
	entry, alive := p.active[key]
	_, spawning := p.spawningKeys[key]
	p.mu.Unlock()
	if spawning || (alive && !entryIdle(entry)) {
		return false
	}
	_, a, ok := p.sessionAgent(sessionID, agentName)
	if !ok {
		return false
	}
	cur := normProviderKey(a.Provider)
	tag, model, follow := "", "", false
	if provider.AgentDefault != nil {
		if t, m, ok := provider.AgentDefault(ctx, sessionID); ok && t != "" {
			tag, model = normProviderKey(t), m
			home, homeModel := cur, a.ModelID
			if a.Failover != nil && a.Failover.HomeProvider != "" {
				home, homeModel = normProviderKey(a.Failover.HomeProvider), a.Failover.HomeModelID
			}
			// Already running there (a stand-in that is the new default)
			// is not moved: ApplySwitch would refuse it as a no-op.
			follow = (home != tag || homeModel != model) && (cur != tag || a.ModelID != model)
		}
	}
	stale := alive && cur != "" && (normProviderKey(entry.provType+"/"+entry.provName) != cur || entry.pinnedModel != a.ModelID)
	if !follow && !stale {
		return false
	}
	var old *provider.Agent
	if alive {
		old = entry.agent
	}
	l := log.With().Str("component", "pool").Str("session", sessionID).Str("agent", agentName).Logger()
	// Held as maybeReturnHome holds a return: a message sent meanwhile
	// waits in the buffer instead of reaching the process about to stop.
	if !p.setFailingOver(key, old) {
		return false
	}
	p.mu.Lock()
	e, live := p.active[key]
	_, spawning = p.spawningKeys[key]
	same := !spawning && ((old == nil && !live) || (old != nil && live && e.agent == old && entryIdle(e)))
	p.mu.Unlock()
	if !same || (old != nil && !old.StopIfIdle()) {
		l.Debug().Bool("same", same).Msg("pool.follow: agent moved on before the stop, staying")
		p.abortReturn(key, sessionID, agentName, old)
		return false
	}
	if old != nil {
		p.releaseSlotIf(key, old)
	}
	var err error
	if follow {
		_, err = provider.ApplySwitch(p.cfg.Layout, sessionID, agentName, tag, provider.SwitchOptions{
			Source:     "agent",
			ModelID:    model,
			ExpectFrom: a.Provider,
			Notify: func(tag string, steps []string) {
				if p.cfg.OnFailoverNotify != nil {
					p.cfg.OnFailoverNotify(sessionID, agentName, provider.SwitchChipText(tag, model), steps)
				}
			},
		})
		switch {
		case errors.Is(err, provider.ErrSwitchedMeanwhile):
			l.Info().Str("to", tag).Msg("pool.follow: switched meanwhile, not following the agent's default")
		case err != nil:
			l.Warn().Err(err).Str("to", tag).Msg("pool.follow: switch to the agent's default failed, staying")
		default:
			l.Info().Str("from", cur).Str("to", tag).Str("model", model).Msg("pool.follow: on the agent's default")
		}
	} else {
		l.Info().Str("provider", cur).Msg("pool.follow: stopped a process left on an older provider")
	}
	// Held messages stay buffered: the caller's own spawn drains them.
	p.endFailingOver(context.Background(), sessionID, agentName, false)
	return err == nil
}
