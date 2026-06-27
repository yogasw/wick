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
	LoadTelegramForUser(userID string) (agentconfig.TelegramChannelConfig, error)
	ListChannelOwners(channelType string) ([]*string, error)
}

// RestStore mirrors SlackStore for the OpenAI-compatible REST channel.
type RestStore interface {
	agentchannels.RestConfigStore
	agentchannels.ChannelEnsurer
	LoadRestForUser(userID string) (agentconfig.RestChannelConfig, error)
	ListChannelOwners(channelType string) ([]*string, error)
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
		ch.SetSessionPrefix(key + ":")
		src := agentslack.NewConfigSourceKeyed(store, ch, uid)
		reg.AddKeyed(key, ch, src)
		if ch.IsConfigured() {
			log.Info().Str("instance", key).Msg("agents: slack channel configured")
		}
	}
}

// Rest registers one keyed REST instance per configured owner, mirroring
// Slack. auth resolves the per-request Bearer (Personal Access Token) and
// is required — without it the channel refuses to serve. The HTTP endpoint
// itself is shared; each keyed instance carries that owner's config row.
func Rest(reg *agentchannels.Registry, store RestStore, sendFn agentchannels.SendFunc, auth agentrest.Authenticator) {
	if err := store.EnsureChannel("rest"); err != nil {
		log.Warn().Err(err).Msg("agents: rest channel ensure failed")
	}
	owners, err := store.ListChannelOwners("rest")
	if err != nil {
		log.Warn().Err(err).Msg("agents: rest list channel owners failed; loading App Owner only")
		owners = []*string{nil}
	}
	for _, ownerID := range owners {
		uid := ""
		if ownerID != nil {
			uid = *ownerID
		}
		cfg, err := store.LoadRestForUser(uid)
		if err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("agents: failed to load rest config for user")
			continue
		}
		ch := agentrest.NewWithOwner(cfg, auth, uid)
		ch.SetSendFunc(sendFn)
		key := instanceKey("rest", ownerID)
		src := agentrest.NewConfigSourceKeyed(store, ch, uid)
		reg.AddKeyed(key, ch, src)
		if ch.IsConfigured() {
			log.Info().Str("instance", key).Msg("agents: rest channel enabled")
		}
	}
}

// Telegram registers one keyed instance per configured owner — see Slack.
func Telegram(reg *agentchannels.Registry, store TelegramStore, sendFn agentchannels.SendFunc) {
	if err := store.EnsureChannel("telegram"); err != nil {
		log.Warn().Err(err).Msg("agents: telegram channel ensure failed")
	}
	owners, err := store.ListChannelOwners("telegram")
	if err != nil {
		log.Warn().Err(err).Msg("agents: telegram list channel owners failed; loading App Owner only")
		owners = []*string{nil}
	}
	for _, ownerID := range owners {
		uid := ""
		if ownerID != nil {
			uid = *ownerID
		}
		cfg, err := store.LoadTelegramForUser(uid)
		if err != nil {
			log.Warn().Err(err).Str("user_id", uid).Msg("agents: failed to load telegram config for user")
			continue
		}
		ch := agenttelegram.NewWithOwner(cfg, uid)
		ch.SetSendFunc(sendFn)
		key := instanceKey("telegram", ownerID)
		ch.SetSessionPrefix(key + ":")
		src := agenttelegram.NewConfigSourceKeyed(store, ch, uid)
		reg.AddKeyed(key, ch, src)
		if ch.IsConfigured() {
			log.Info().Str("instance", key).Msg("agents: telegram channel configured")
		}
	}
}
