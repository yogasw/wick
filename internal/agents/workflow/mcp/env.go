package mcp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// EnvGetResult is the workflow_env_get response.
type EnvGetResult struct {
	Schema     []workflow.EnvField `json:"schema"`
	Values     map[string]string   `json:"values"`
	SecretKeys []string            `json:"secret_keys,omitempty"`
}

// EnvGet returns the env schema from the draft and the stored env values.
// Secret values are masked as "••••••••" so plaintext never leaves the server.
func (m *Ops) EnvGet(id string) (EnvGetResult, error) {
	if m.Service == nil {
		return EnvGetResult{}, errors.New("mcp: service not wired")
	}
	w, err := m.Service.LoadDraft(id)
	if err != nil {
		return EnvGetResult{}, fmt.Errorf("load workflow %s: %w", id, err)
	}
	values, err := m.Service.LoadEnvValues(id)
	if err != nil {
		return EnvGetResult{}, fmt.Errorf("load env values: %w", err)
	}

	secretKeys := map[string]bool{}
	for _, f := range w.Env {
		if f.IsSecret() {
			secretKeys[f.Name] = true
		}
	}
	// Free-form wick_enc_ / wick_cenc_ tokens are also secrets.
	for k, v := range values {
		if strings.HasPrefix(v, "wick_enc_") || strings.HasPrefix(v, "wick_cenc_") {
			secretKeys[k] = true
		}
	}

	// Apply schema defaults.
	for _, f := range w.Env {
		if _, ok := values[f.Name]; !ok && f.Default != "" {
			values[f.Name] = f.Default
		}
	}

	masked := make(map[string]string, len(values))
	secretKeysList := []string{}
	for k, v := range values {
		if secretKeys[k] {
			masked[k] = "••••••••"
			secretKeysList = append(secretKeysList, k)
		} else {
			masked[k] = v
		}
	}

	return EnvGetResult{
		Schema:     w.Env,
		Values:     masked,
		SecretKeys: secretKeysList,
	}, nil
}

// EnvSet merges one or more key/value pairs into the stored env values.
// Existing keys not in the incoming map are preserved (partial update).
// Secret keys (schema widget=secret OR keys in secretKeys) whose value is
// plaintext are stored as-is — the connector layer does not encrypt; the
// caller is responsible for passing wick_enc_ tokens for secrets when
// encryption is required.
func (m *Ops) EnvSet(id string, values map[string]string) error {
	if m.Service == nil {
		return errors.New("mcp: service not wired")
	}
	existing, _ := m.Service.LoadEnvValues(id)
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range values {
		existing[k] = v
	}
	return m.Service.SaveEnvValues(id, existing)
}

// EnvDelete removes one or more keys from the stored env values.
// Keys not present are silently ignored.
func (m *Ops) EnvDelete(id string, keys []string) error {
	if m.Service == nil {
		return errors.New("mcp: service not wired")
	}
	values, err := m.Service.LoadEnvValues(id)
	if err != nil {
		return fmt.Errorf("load env values: %w", err)
	}
	for _, k := range keys {
		delete(values, k)
	}
	return m.Service.SaveEnvValues(id, values)
}
