// Package setup composes channel implementations into a registry.
//
// Lives in its own package to break the import cycle: channels root and
// each transport subpackage (slack, telegram, …) all need to be imported
// here, but they cannot import each other. Server calls one Setup* per
// channel and is done — no per-channel boilerplate left in api/server.go.
//
// Adding a new channel: write the subpackage (e.g. channels/discord),
// then add a `Discord(...)` composer here. Server picks it up by adding
// one new line.
package setup

import (
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentrest "github.com/yogasw/wick/internal/agents/channels/rest"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agenttelegram "github.com/yogasw/wick/internal/agents/channels/telegram"
)

// SlackStore is the full set of capabilities the Slack composer needs:
// load the config + ensure the agent_channels row exists. Composed from
// the smaller interfaces declared in the channels package so a test fake
// only has to satisfy these two methods.
type SlackStore interface {
	agentchannels.SlackConfigStore
	agentchannels.ChannelEnsurer
}

// TelegramStore mirrors SlackStore.
type TelegramStore interface {
	agentchannels.TelegramConfigStore
	agentchannels.ChannelEnsurer
}

// RestStore mirrors SlackStore for the OpenAI-compatible REST channel.
type RestStore interface {
	agentchannels.RestConfigStore
	agentchannels.ChannelEnsurer
}

// Store is the union of every per-channel store interface used by All.
// DBStore satisfies it; tests can build a smaller fake by composing only
// the per-channel interfaces they need (e.g. just SlackStore).
type Store interface {
	SlackStore
	TelegramStore
	RestStore
}

// SendFnFactory builds a per-channel SendFunc. Setup composers call it
// with the channel's name so each transport gets a closure bound to its
// own workspace lookup. Server provides one factory; setup distributes
// per-channel closures to each composer.
type SendFnFactory func(channelName string) agentchannels.SendFunc

// All registers every built-in channel on reg in one call. Adding a new
// channel = write its subpackage + composer here + extend this function.
// Server.go never changes after this hook is in place.
func All(reg *agentchannels.Registry, store Store, sendFn SendFnFactory, restAuth agentrest.Authenticator) {
	Slack(reg, store, sendFn("slack"))
	Telegram(reg, store, sendFn("telegram"))
	Rest(reg, store, sendFn("rest"), restAuth)
}

// Slack constructs the Slack channel from the store, applies its setters,
// and registers it (with a hot-reload ConfigSource) on reg. Returns the
// channel for callers that need to retain a reference; safe to ignore.
//
// Logs whether the channel is configured so operators see at boot why a
// transport may stay dormant.
func Slack(reg *agentchannels.Registry, store SlackStore, sendFn agentchannels.SendFunc) *agentslack.Channel {
	if err := store.EnsureChannel("slack"); err != nil {
		log.Warn().Err(err).Msg("agents: slack channel ensure failed")
	}
	cfg, pubURL, err := store.LoadSlack()
	if err != nil {
		log.Warn().Err(err).Msg("agents: failed to load slack config from agent_channels")
	}
	ch := agentslack.New(cfg)
	ch.SetSendFunc(sendFn)
	ch.SetPublicURL(pubURL)
	reg.Add(ch, agentslack.NewConfigSource(store, ch))
	if ch.IsConfigured() {
		log.Info().Msg("agents: slack channel configured, will start with server")
	} else {
		log.Info().Msg("agents: slack channel not configured, skipping (set BotToken + AppToken in Channels → Slack)")
	}
	return ch
}

// Rest constructs the OpenAI-compatible REST channel and registers it
// with a hot-reload ConfigSource. auth resolves the per-request Bearer
// (Personal Access Token) and is required — without it the channel
// refuses to serve.
func Rest(reg *agentchannels.Registry, store RestStore, sendFn agentchannels.SendFunc, auth agentrest.Authenticator) *agentrest.Channel {
	if err := store.EnsureChannel("rest"); err != nil {
		log.Warn().Err(err).Msg("agents: rest channel ensure failed")
	}
	cfg, err := store.LoadRest()
	if err != nil {
		log.Warn().Err(err).Msg("agents: failed to load rest config from agent_channels")
	}
	ch := agentrest.New(cfg, auth)
	ch.SetSendFunc(sendFn)
	reg.Add(ch, agentrest.NewConfigSource(store, ch))
	if ch.IsConfigured() {
		log.Info().Msg("agents: rest channel enabled, serving POST /integrations/rest/v1/chat/completions")
	} else {
		log.Info().Msg("agents: rest channel not enabled (toggle in Channels → REST)")
	}
	return ch
}

// Telegram mirrors Slack — see that comment.
func Telegram(reg *agentchannels.Registry, store TelegramStore, sendFn agentchannels.SendFunc) *agenttelegram.Channel {
	if err := store.EnsureChannel("telegram"); err != nil {
		log.Warn().Err(err).Msg("agents: telegram channel ensure failed")
	}
	cfg, err := store.LoadTelegram()
	if err != nil {
		log.Warn().Err(err).Msg("agents: failed to load telegram config from agent_channels")
	}
	ch := agenttelegram.New(cfg)
	ch.SetSendFunc(sendFn)
	reg.Add(ch, agenttelegram.NewConfigSource(store, ch))
	if ch.IsConfigured() {
		log.Info().Msg("agents: telegram channel configured, will start with server")
	} else {
		log.Info().Msg("agents: telegram channel not configured, skipping (set BotToken in Channels → Telegram)")
	}
	return ch
}
