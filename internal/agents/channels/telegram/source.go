// Package telegram — ConfigSource: hot-reload glue. Mirrors slack/source.go.

package telegram

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// ConfigSource implements agentchannels.ConfigSource for *Channel.
type ConfigSource struct {
	store  agentchannels.TelegramConfigStore
	ch     *Channel
	loadFn func() agentconfig.TelegramChannelConfig // optional override
}

// NewConfigSource binds a Telegram channel to a config store so the
// registry watcher can hot-reload it.
func NewConfigSource(store agentchannels.TelegramConfigStore, ch *Channel) *ConfigSource {
	return &ConfigSource{store: store, ch: ch}
}

// NewConfigSourceKeyed creates a ConfigSource that loads config for a specific
// user's Telegram channel row. userID="" loads the App Owner row.
func NewConfigSourceKeyed(store agentchannels.TelegramConfigStore, ch *Channel, userID string) *ConfigSource {
	type perUserLoader interface {
		LoadTelegramForUser(string) (agentconfig.TelegramChannelConfig, error)
	}
	if loader, ok := store.(perUserLoader); ok {
		return &ConfigSource{
			store: store,
			ch:    ch,
			loadFn: func() agentconfig.TelegramChannelConfig {
				cfg, err := loader.LoadTelegramForUser(userID)
				if err != nil {
					return agentconfig.TelegramChannelConfig{}
				}
				return cfg
			},
		}
	}
	return NewConfigSource(store, ch)
}

func (s *ConfigSource) load() agentconfig.TelegramChannelConfig {
	if s.loadFn != nil {
		return s.loadFn()
	}
	cfg, err := s.store.LoadTelegram()
	if err != nil {
		return agentconfig.TelegramChannelConfig{}
	}
	return cfg
}

// Hash fingerprints fields that meaningfully affect connection state.
func (s *ConfigSource) Hash() string {
	cfg := s.load()
	return cfg.BotToken + "|" + cfg.AllowedIDs + "|" + cfg.ProjectID
}

// Reload re-reads the config and applies it to the bound channel.
func (s *ConfigSource) Reload(ctx context.Context) error {
	cfg := s.load()
	s.ch.Reload(ctx, cfg)
	return nil
}
