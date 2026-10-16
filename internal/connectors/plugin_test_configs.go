package connectors

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/enc"
)

// PlainConfigsForPluginTest returns instance id's credential map in
// plaintext, decrypted exactly as Execute would for userID. It exists for
// one caller only — an admin-approved plugin test request (see
// internal/admin/plugin_tests.go) — which hands the map to a locally run
// plugin binary. Every other path keeps configs inside wick.
func (s *Service) PlainConfigsForPluginTest(ctx context.Context, id, userID string) (map[string]string, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("connector not found: %w", err)
	}
	cfg := s.LoadConfigs(*c)
	if s.enc == nil || s.enc.Disabled() {
		return cfg, nil
	}
	for k, v := range cfg {
		if !enc.IsMasterToken(v) {
			continue
		}
		plain, err := s.enc.DecryptMaster(v)
		if err != nil {
			return nil, fmt.Errorf("decrypt config %q: %w", k, err)
		}
		cfg[k] = plain
	}
	out, _, err := unmaskMap(s.enc, cfg, userID)
	if err != nil {
		return nil, fmt.Errorf("decrypt configs: %w", err)
	}
	return out, nil
}
