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

// BotIdentityStore is the optional cache a store may provide for a
// channel's resolved bot identity (id, display name, workspace). Kept
// out of the per-channel Store interfaces so existing fakes in tests
// keep compiling — a store that doesn't implement it simply resolves the
// identity from the provider on every boot.
type BotIdentityStore interface {
	LoadBotIdentity(channelType, userID string) (botUserID, botName, workspace string, err error)
	SaveBotIdentity(channelType, userID, botUserID, botName, workspace string) error
}

// WireIdentityCache connects an instance to the identity cache on its
// own channel row: seed what was cached, persist whatever it resolves,
// and flush an identity the constructor already fetched.
//
// Deliberately generic over the channel: a transport added later gets
// caching — and named "<Channel> - <bot> - <owner>" rows in every picker
// — the moment it implements agentchannels.IdentityCache. Nothing here,
// in the workflow catalog, or in the palette needs a per-channel branch.
// A store or channel that doesn't implement the seam is a silent no-op.
// Exported so the dashboard's hot-add path (a channel configured while
// wick runs) wires exactly what boot wires.
func WireIdentityCache(store any, channelType, ownerUserID string, ch agentchannels.Channel) {
	idStore, ok := store.(BotIdentityStore)
	if !ok {
		return
	}
	cache, ok := ch.(agentchannels.IdentityCache)
	if !ok {
		return
	}
	save := func(botID, botName, workspace string) {
		if err := idStore.SaveBotIdentity(channelType, ownerUserID, botID, botName, workspace); err != nil {
			log.Warn().Err(err).
				Str("channel", channelType).
				Str("user_id", ownerUserID).
				Msg("agents: caching bot identity failed")
		}
	}
	if botID, botName, workspace, err := idStore.LoadBotIdentity(channelType, ownerUserID); err == nil {
		cache.SeedIdentity(botID, botName, workspace)
	}
	cache.SetIdentitySink(save)
	// The constructor may already have resolved an identity (Telegram's
	// getMe runs there) — with no sink wired yet, so persist it now.
	if botID, botName, workspace := cache.BotIdentity(); botID != "" || botName != "" {
		save(botID, botName, workspace)
	}
}

// loadCachedIdentity reads a cached identity without wiring anything —
// for a channel that wants it BEFORE construction (Slack, whose
// constructor would otherwise spend an auth.test resolving what is
// already known).
func loadCachedIdentity(store any, channelType, ownerUserID string) (botID, botName, workspace string) {
	idStore, ok := store.(BotIdentityStore)
	if !ok {
		return "", "", ""
	}
	botID, botName, workspace, err := idStore.LoadBotIdentity(channelType, ownerUserID)
	if err != nil {
		return "", "", ""
	}
	return botID, botName, workspace
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

// sessionPrefix derives a charset-safe session-key prefix that namespaces an
// instance's session ids (the prefix becomes part of the on-disk session
// folder name and the pool/turns key). ":" from the registry key is illegal
// in session ids ([A-Za-z0-9._-]) and Windows filenames, so it never appears
// here.
//
//   - App Owner instance (no owner user): "<channelType>-" — the cleaner
//     "slack-<ts>" form, dropping the noisy "__owner__" segment.
//   - Per-user instance: "<channelType>-<ownerUserID>-" — keeps the owner so
//     two users' bots (possibly in different workspaces) never collide on a
//     shared threadTS.
//
// The two forms can't collide: an owner user id is a UUID, never empty, so a
// per-user prefix is always longer and distinct from the bare App Owner one.
func sessionPrefix(channelType string, ownerUserID *string) string {
	owner := ""
	if ownerUserID != nil {
		owner = *ownerUserID
	}
	return SessionPrefix(channelType, owner)
}

// SessionPrefix is the exported form used by the hot-reload path
// (tools/agents) so boot-time and dynamic-add channels agree on the exact
// prefix. ownerUserID "" = the App Owner instance.
func SessionPrefix(channelType, ownerUserID string) string {
	if ownerUserID == "" {
		return channelType + "-"
	}
	return channelType + "-" + ownerUserID + "-"
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
		// Read the cached identity BEFORE constructing: with it in hand the
		// channel skips the auth.test its constructor would otherwise run.
		cachedBotID, cachedBotName, cachedTeam := loadCachedIdentity(store, "slack", uid)
		ch := agentslack.NewWithOwnerCached(cfg, uid, cachedBotID, cachedBotName, cachedTeam)
		WireIdentityCache(store, "slack", uid, ch)
		ch.SetSendFunc(sendFn)
		ch.SetPublicURL(pubURL)
		key := instanceKey("slack", ownerID)
		ch.SetSessionPrefix(sessionPrefix("slack", ownerID))
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
		WireIdentityCache(store, "rest", uid, ch)
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
		WireIdentityCache(store, "telegram", uid, ch)
		ch.SetSendFunc(sendFn)
		key := instanceKey("telegram", ownerID)
		ch.SetSessionPrefix(sessionPrefix("telegram", ownerID))
		src := agenttelegram.NewConfigSourceKeyed(store, ch, uid)
		reg.AddKeyed(key, ch, src)
		if ch.IsConfigured() {
			log.Info().Str("instance", key).Msg("agents: telegram channel configured")
		}
	}
}
