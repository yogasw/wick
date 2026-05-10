// Package telegram — ConfigSource: hot-reload glue. Mirrors slack/source.go.

package telegram

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// ConfigSource implements agentchannels.ConfigSource for *Channel.
type ConfigSource struct {
	store agentchannels.TelegramConfigStore
	ch    *Channel
}

// NewConfigSource binds a Telegram channel to a config store so the
// registry watcher can hot-reload it.
func NewConfigSource(store agentchannels.TelegramConfigStore, ch *Channel) *ConfigSource {
	return &ConfigSource{store: store, ch: ch}
}

func (s *ConfigSource) load() agentconfig.TelegramChannelConfig {
	cfg, err := s.store.LoadTelegram()
	if err != nil {
		return agentconfig.TelegramChannelConfig{}
	}
	return cfg
}

// Hash fingerprints fields that meaningfully affect connection state.
func (s *ConfigSource) Hash() string {
	cfg := s.load()
	return cfg.BotToken + "|" + cfg.AllowedIDs + "|" + cfg.Workspace
}

// Reload re-reads the config and applies it to the bound channel.
func (s *ConfigSource) Reload(ctx context.Context) error {
	cfg := s.load()
	s.ch.Reload(ctx, cfg)
	return nil
}
