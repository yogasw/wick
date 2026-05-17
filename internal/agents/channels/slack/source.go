// Package slack — ConfigSource: hot-reload glue for Channel.
//
// Loads via the SlackConfigStore abstraction so the source never imports
// gorm directly. Server wires a DB-backed implementor at boot, tests can
// swap a fake. Triggers Channel.Reload when the fingerprint of
// connection-affecting fields changes.

package slack

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// ConfigSource implements agentchannels.ConfigSource for *Channel.
type ConfigSource struct {
	store agentchannels.SlackConfigStore
	ch    *Channel
}

// NewConfigSource binds a Slack channel to a config store so the
// registry watcher can hot-reload it.
func NewConfigSource(store agentchannels.SlackConfigStore, ch *Channel) *ConfigSource {
	return &ConfigSource{store: store, ch: ch}
}

func (s *ConfigSource) load() (agentconfig.SlackChannelConfig, string) {
	cfg, pubURL, err := s.store.LoadSlack()
	if err != nil {
		return agentconfig.SlackChannelConfig{}, ""
	}
	return cfg, pubURL
}

// Hash fingerprints fields that materially affect connection state.
func (s *ConfigSource) Hash() string {
	cfg, pubURL := s.load()
	return cfg.Mode + "|" + cfg.BotToken + "|" + cfg.AppToken + "|" +
		cfg.SigningSecret + "|" + pubURL + "|" +
		cfg.UsersMode + "|" + cfg.AllowedUsers + "|" +
		cfg.GroupsMode + "|" + cfg.AllowedGroups + "|" +
		cfg.ChannelsMode + "|" + cfg.AllowedChannels + "|" +
		cfg.GateApprovers + "|" + cfg.GateApproverUsers + "|" + cfg.GateApproverGroups
}

// Reload re-reads the config and applies it to the bound channel.
// Also refreshes OAuth ClientID/ClientSecret so a credential change
// in the channel settings page takes effect without a restart.
func (s *ConfigSource) Reload(ctx context.Context) error {
	cfg, pubURL := s.load()
	s.ch.Reload(ctx, cfg, pubURL)

	// Preserve the existing OnTokenSaved callback; only update the
	// credential fields that live in the channel config.
	if cfg.ClientID != "" || cfg.ClientSecret != "" {
		s.ch.cfgMu.Lock()
		existing := s.ch.oauthCfg
		s.ch.cfgMu.Unlock()
		existing.ClientID = cfg.ClientID
		existing.ClientSecret = cfg.ClientSecret
		s.ch.SetOAuthConfig(existing)
	}
	return nil
}
