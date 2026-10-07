package agents

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// modelsets_wick.go registers wick as the first provider.ModelSets: the
// instance's registered models with each LIVE SET as a one-segment path,
// expanded against the vendor list. It wraps the existing picker helpers so
// the behaviour is exactly what the `if TypeWick` branch did before; the
// engine keeps its own pin resolver (pickModel) since it calls the vendor SDK
// itself instead of passing --model to a CLI.

func init() { provider.RegisterModelSets(provider.TypeWick, wickModelSets{}) }

type wickModelSets struct{}

func (wickModelSets) Sets(_ context.Context, ins provider.Instance) ([]provider.ModelChoice, error) {
	return choicesFromVM(modelChoicesFor(ins)), nil
}

func (wickModelSets) Expand(ctx context.Context, ins provider.Instance, path []string) ([]provider.ModelChoice, error) {
	if len(path) != 1 {
		return nil, nil // wick sets are one level deep
	}
	return choicesFromVM(expandLiveWickSet(ctx, ins, path[0], "")), nil
}

func (wickModelSets) Resolve(ins provider.Instance, path []string, model string) (provider.SpawnPin, error) {
	if len(path) != 1 {
		return provider.SpawnPin{}, fmt.Errorf("wick: pin path must be one live-set entry, got %d segments", len(path))
	}
	for _, m := range ins.WickModels {
		if m.ID == path[0] && !m.Disabled && m.LiveSet {
			return provider.SpawnPin{Model: model, Provider: m.ID}, nil
		}
	}
	return provider.SpawnPin{}, fmt.Errorf("wick: no enabled live set %q", path[0])
}

func choicesFromVM(ms []view.ModelChoiceVM) []provider.ModelChoice {
	if ms == nil {
		return nil
	}
	out := make([]provider.ModelChoice, 0, len(ms))
	for _, m := range ms {
		out = append(out, provider.ModelChoice{ID: m.ID, Label: m.Label, Default: m.Default, Desc: m.Desc, Live: m.Live, Caps: m.Caps})
	}
	return out
}
