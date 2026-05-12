// Package rest — ConfigSource: hot-reload glue. Mirrors slack/source.go.

package rest

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// ConfigSource implements agentchannels.ConfigSource for *Channel.
type ConfigSource struct {
	store agentchannels.RestConfigStore
	ch    *Channel
}

// NewConfigSource binds a REST channel to a config store so the registry
// watcher can hot-reload it.
func NewConfigSource(store agentchannels.RestConfigStore, ch *Channel) *ConfigSource {
	return &ConfigSource{store: store, ch: ch}
}

func (s *ConfigSource) load() agentconfig.RestChannelConfig {
	cfg, err := s.store.LoadRest()
	if err != nil {
		return agentconfig.RestChannelConfig{}
	}
	return cfg
}

// Hash fingerprints fields that materially affect serving state.
func (s *ConfigSource) Hash() string {
	cfg := s.load()
	return cfg.Enabled + "|" + cfg.Workspace
}

// Reload re-reads config and applies it to the bound channel.
func (s *ConfigSource) Reload(ctx context.Context) error {
	cfg := s.load()
	s.ch.Reload(ctx, cfg)
	return nil
}
