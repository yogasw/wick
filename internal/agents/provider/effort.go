package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// EffortDefault delegates to the selected provider/model's default. An empty
// stored selection instead inherits the next enclosing configuration.
const EffortDefault = "default"

// EffortOption is a native effort ID, not a universal token budget.
type EffortOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// EffortCapabilities describes a model on a specific provider instance.
// Default is empty when unknown. The UI supplies the implicit Default option.
type EffortCapabilities struct {
	Options []EffortOption `json:"options"`
	Default string         `json:"default,omitempty"`
	Source  string         `json:"source,omitempty"`
}

// EffortResolver receives the full pin, including account/live-set path.
// Implementations should use cached metadata or bounded context-aware probes.
type EffortResolver func(ctx context.Context, ins Instance, modelID string) (EffortCapabilities, error)

var (
	effortMu        sync.RWMutex
	effortResolvers = map[Type]EffortResolver{}
)

// RegisterEffortResolver registers the provider's capability callback.
// A nil resolver removes the registration, following RegisterModelSets.
func RegisterEffortResolver(t Type, resolve EffortResolver) {
	effortMu.Lock()
	defer effortMu.Unlock()
	if resolve == nil {
		delete(effortResolvers, t)
		return
	}
	effortResolvers[t] = resolve
}

// EffortCapabilitiesFor delegates to the provider. registered=false means
// unknown; a registered resolver with zero options means not configurable.
func EffortCapabilitiesFor(ctx context.Context, ins Instance, modelID string) (caps EffortCapabilities, registered bool, err error) {
	effortMu.RLock()
	resolve := effortResolvers[ins.Type]
	effortMu.RUnlock()
	if resolve == nil {
		return EffortCapabilities{}, false, nil
	}
	caps, err = resolve(ctx, ins, modelID)
	if err != nil {
		return EffortCapabilities{}, true, err
	}
	if err = validateEffortCapabilities(caps); err != nil {
		return EffortCapabilities{}, true, err
	}
	caps.Options = append([]EffortOption{}, caps.Options...)
	return caps, true, nil
}

func validateEffortCapabilities(caps EffortCapabilities) error {
	seen := map[string]bool{}
	for _, option := range caps.Options {
		if option.ID == "" || strings.TrimSpace(option.ID) != option.ID || option.ID == EffortDefault || seen[option.ID] {
			return fmt.Errorf("invalid or duplicate effort option %q", option.ID)
		}
		seen[option.ID] = true
	}
	if caps.Default != "" && !seen[caps.Default] {
		return fmt.Errorf("default effort %q is not an offered option", caps.Default)
	}
	return nil
}

// EffortSelection distinguishes saved choice from known effective effort.
// Native is empty when no explicit override should be emitted.
type EffortSelection struct {
	Requested string `json:"requested"`
	Effective string `json:"effective,omitempty"`
	Native    string `json:"-"`
}

// ResolveEffortSelection checks choices in caller-supplied precedence order.
// Explicit Default stops inheritance. Unsupported choices are never silently
// translated to a different effort.
func ResolveEffortSelection(caps EffortCapabilities, choices ...string) (EffortSelection, error) {
	for _, choice := range choices {
		if choice == "" {
			continue
		}
		if choice == EffortDefault {
			break
		}
		for _, option := range caps.Options {
			if option.ID == choice {
				return EffortSelection{Requested: choice, Effective: choice, Native: choice}, nil
			}
		}
		return EffortSelection{}, fmt.Errorf("effort %q is not supported by the selected model", choice)
	}
	return EffortSelection{Requested: EffortDefault, Effective: caps.Default}, nil
}
