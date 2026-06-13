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
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// SlackStore is the full set of capabilities the Slack composer needs.
type SlackStore interface {
	agentchannels.SlackConfigStore
	agentchannels.ChannelEnsurer
	LoadSlackForUser(userID string) (agentconfig.SlackChannelConfig, string, error)
	ListChannelOwners(channelType string) ([]*string, error)
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

func instanceKey(channelType string, ownerUserID *string) string {
	if ownerUserID == nil || *ownerUserID == "" {
		return channelType + ":__owner__"
	}
	return channelType + ":" + *ownerUserID
}

// Slack loads all configured user channel rows and registers one keyed instance per user.
func Slack(reg *agentchannels.Registry, store SlackStore, sendFn agentchannels.SendFunc) {
	if err := store.EnsureChannel("slack"); err != nil {
		log.Warn().Err(err).Msg("agents: slack channel ensure failed")
	}
	owners, err := store.ListChannelOwners("slack")
	if err != nil {
		log.Warn().Err(err).Msg("agents: slack list channel owners failed; loading App Owner only")
		owners = []*string{nil}
	}
	for _, ownerID := range owners {
		uid := ""
		if ownerID != nil {
			uid = *ownerID
		}
		cfg, pubURL, err := store.LoadSlackForUser(uid)
		if err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("agents: failed to load slack config for user")
			continue
		}
		ch := agentslack.NewWithOwner(cfg, uid)
		ch.SetSendFunc(sendFn)
		ch.SetPublicURL(pubURL)
		key := instanceKey("slack", ownerID)
		src := agentslack.NewConfigSourceKeyed(store, ch, uid)
		reg.AddKeyed(key, ch, src)
		if ch.IsConfigured() {
			log.Info().Str("instance", key).Msg("agents: slack channel configured")
		}
	}
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
		log.Info().Msg("agents: rest channel enabled, serving POST /integrations/rest/api/v1/openai/chat/completions")
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
