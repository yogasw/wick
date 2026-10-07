package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yogasw/wick/pkg/entity"
)

// SecretMask, like "", sent back for a secret keeps the stored value (for
// clients that echo a masked placeholder).
const SecretMask = "••••••••"

// ConfigStore is the slice of the configs service a host needs.
type ConfigStore interface {
	EnsureOwned(ctx context.Context, owner string, rows ...entity.Config) error
	ListOwned(owner string) []entity.Config
	SetOwned(ctx context.Context, owner, key, value string) error
}

// ConfigOwner is the configs owner of service key's rows.
func ConfigOwner(key string) string { return "service_plugin:" + key }

// ConfigField is one config row as the admin page shows it: a secret's
// value never leaves the server, only HasValue tells one is stored.
type ConfigField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type,omitempty"`
	Options     string `json:"options,omitempty"`
	Description string `json:"description,omitempty"`
	IsSecret    bool   `json:"is_secret"`
	HasValue    bool   `json:"has_value"`
	Required    bool   `json:"required"`
}

// configValues is what the plugin receives: plaintext values by key.
func (h *Host) configValues(key string) map[string]string {
	if h.Config != nil {
		return h.Config(key)
	}
	out := map[string]string{}
	if h.Configs == nil {
		return out
	}
	for _, c := range h.Configs.ListOwned(ConfigOwner(key)) {
		out[c.Key] = c.Value
	}
	return out
}

// configFields lists s's config rows in manifest order, secrets masked.
func (h *Host) configFields(s *Service) []ConfigField {
	stored := map[string]entity.Config{}
	if h.Configs != nil {
		for _, c := range h.Configs.ListOwned(ConfigOwner(s.Key)) {
			stored[c.Key] = c
		}
	}
	out := make([]ConfigField, 0, len(s.Manifest.Configs))
	for _, d := range s.Manifest.Configs {
		c, ok := stored[d.Key]
		if !ok {
			c = d
		}
		v := c.Value
		if d.IsSecret {
			v = ""
		}
		out = append(out, ConfigField{Key: d.Key, Value: v, Type: d.Type, Options: d.Options,
			Description: d.Description, IsSecret: d.IsSecret, HasValue: c.Value != "", Required: d.Required})
	}
	return out
}

// SetConfig stores values (manifest keys only; an empty or masked secret
// keeps the stored one) and pushes the new config to the running process.
func (h *Host) SetConfig(ctx context.Context, s *Service, values map[string]string) error {
	if h.Configs == nil {
		return fmt.Errorf("config store not available")
	}
	decl := map[string]entity.Config{}
	for _, d := range s.Manifest.Configs {
		decl[d.Key] = d
	}
	for k := range values {
		if _, ok := decl[k]; !ok {
			return fmt.Errorf("unknown config %q", k)
		}
	}
	for _, d := range s.Manifest.Configs {
		v, ok := values[d.Key]
		if !ok || (d.IsSecret && (v == "" || v == SecretMask)) {
			continue
		}
		if err := h.Configs.SetOwned(ctx, ConfigOwner(s.Key), d.Key, v); err != nil {
			return err
		}
	}
	s.Sup.Reconfigure()
	return nil
}

func (h *Host) serveSetConfig(w http.ResponseWriter, r *http.Request, s *Service) {
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := h.SetConfig(r.Context(), s, body.Values); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.View(s, true))
}
