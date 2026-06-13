// Package sessionconfig stores per-session connector config
// overrides — the backing store for the wick_session_config MCP
// tool. Overrides live in config_overrides.json inside the session
// dir, so they survive a server restart but die with the session.
//
// Values are stored exactly as given; callers are responsible for
// encrypting secrets into wick_enc_ tokens BEFORE calling Set —
// nothing in this package ever sees or persists plaintext secrets.
package sessionconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// mu serializes read-modify-write cycles on the overrides file.
// Per-session granularity isn't worth the bookkeeping — overrides
// change at human speed (a modal submit), never in a hot path.
var mu sync.Mutex

// Overrides maps connectorID → configKey → value.
type Overrides map[string]map[string]string

// Load reads the full overrides map for one session. A missing file
// is an empty map, not an error.
func Load(layout agentconfig.Layout, sessionID string) (Overrides, error) {
	b, err := os.ReadFile(layout.SessionConfigOverrides(sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return Overrides{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config overrides: %w", err)
	}
	var out Overrides
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse config overrides: %w", err)
	}
	if out == nil {
		out = Overrides{}
	}
	return out, nil
}

// For returns the override map for one connector in one session.
// Missing file or connector yields an empty map.
func For(layout agentconfig.Layout, sessionID, connectorID string) (map[string]string, error) {
	all, err := Load(layout, sessionID)
	if err != nil {
		return nil, err
	}
	if m, ok := all[connectorID]; ok {
		return m, nil
	}
	return map[string]string{}, nil
}

// Set merges values into the connector's override map and persists.
// Existing keys not present in values are kept.
func Set(layout agentconfig.Layout, sessionID, connectorID string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	all, err := Load(layout, sessionID)
	if err != nil {
		return err
	}
	m := all[connectorID]
	if m == nil {
		m = make(map[string]string, len(values))
	}
	for k, v := range values {
		m[k] = v
	}
	all[connectorID] = m
	return save(layout, sessionID, all)
}

// Clear removes overrides. Empty keys removes the whole connector
// entry; otherwise only the named keys. Returns the keys actually
// removed.
func Clear(layout agentconfig.Layout, sessionID, connectorID string, keys []string) ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := Load(layout, sessionID)
	if err != nil {
		return nil, err
	}
	m, ok := all[connectorID]
	if !ok {
		return nil, nil
	}
	var removed []string
	if len(keys) == 0 {
		for k := range m {
			removed = append(removed, k)
		}
		delete(all, connectorID)
	} else {
		for _, k := range keys {
			if _, ok := m[k]; ok {
				delete(m, k)
				removed = append(removed, k)
			}
		}
		if len(m) == 0 {
			delete(all, connectorID)
		} else {
			all[connectorID] = m
		}
	}
	return removed, save(layout, sessionID, all)
}

func save(layout agentconfig.Layout, sessionID string, all Overrides) error {
	path := layout.SessionConfigOverrides(sessionID)
	if len(all) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove config overrides: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config overrides: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write config overrides: %w", err)
	}
	return nil
}
