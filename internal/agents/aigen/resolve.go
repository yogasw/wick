package aigen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

// typed is satisfied by providers that know their runtime type; the
// instance name alone does not say it (an instance may be called "work").
type typed interface{ ProviderType() string }

// ProviderType returns p's runtime type, or its name when it can't say.
func ProviderType(p wfprovider.Provider) string {
	if t, ok := p.(typed); ok && t.ProviderType() != "" {
		return t.ProviderType()
	}
	return p.Name()
}

// ListResolver builds a Resolver over a live provider list. Only
// structured-output providers qualify. A named provider must exist; with
// no name, defaultKey ("type" or "type/name", read live — the operator
// changes it in Settings) picks one, else the first that qualifies.
func ListResolver(list func() ([]wfprovider.Provider, error), defaultKey func() string) Resolver {
	return func(_ context.Context, _ string, name string) (Resolved, error) {
		all, err := list()
		if err != nil {
			return Resolved{}, err
		}
		var usable []wfprovider.Provider
		for _, p := range all {
			if p != nil && p.Capabilities().StructuredOutput {
				usable = append(usable, p)
			}
		}
		if len(usable) == 0 {
			return Resolved{}, ErrNoProvider
		}
		pick := func(p wfprovider.Provider) Resolved {
			return Resolved{Provider: p, Type: ProviderType(p), Name: p.Name()}
		}
		if name = strings.TrimSpace(name); name != "" {
			for _, p := range usable {
				if p.Name() == name {
					return pick(p), nil
				}
			}
			return Resolved{}, fmt.Errorf("AI provider %q is unavailable — pick another provider", name)
		}
		if defaultKey != nil {
			if t, n := splitKey(defaultKey()); t != "" {
				for _, p := range usable {
					if ProviderType(p) == t && p.Name() == n {
						return pick(p), nil
					}
				}
				for _, p := range usable {
					if ProviderType(p) == t {
						return pick(p), nil
					}
				}
			}
		}
		return pick(usable[0]), nil
	}
}

// splitKey reads an instance key. The setting is a picker that may store
// a JSON list of {id,name}; the first id wins. "claude" = type claude,
// instance claude.
func splitKey(raw string) (pType, pName string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var picks []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &picks) != nil || len(picks) == 0 {
			return "", ""
		}
		raw = strings.TrimSpace(picks[0].ID)
	}
	if raw == "" {
		return "", ""
	}
	if i := strings.Index(raw, "/"); i >= 0 {
		return raw[:i], raw[i+1:]
	}
	return raw, raw
}
