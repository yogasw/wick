package opencode

import (
	"context"

	"github.com/yogasw/wick/internal/agents/provider"
)

// opencode's reasoning effort is a per-model "variant" (`--variant`), and
// each model lists its own: read them from the instance's catalog (the
// opencode server's GET /provider). The effort cache keeps the answer per
// model, so the catalog is not asked again on every render.
func init() {
	provider.RegisterEffortResolver(provider.TypeOpencode, resolveEffort)
}

func resolveEffort(ctx context.Context, ins provider.Instance, modelID string) (provider.EffortCapabilities, error) {
	cat := provider.PeekOpencodeCatalog(ins)
	if cat == nil {
		var err error
		if cat, err = provider.FetchOpencodeCatalog(ctx, ins); err != nil {
			return provider.EffortCapabilities{}, err
		}
	}
	caps := provider.EffortCapabilities{Source: "opencode catalog"}
	seed, ok := catalogModel(cat, modelID)
	if !ok {
		return caps, nil
	}
	for _, v := range seed.Variants {
		caps.Options = append(caps.Options, provider.EffortOption{ID: v, Label: provider.EffortLabel(v)})
	}
	return caps, nil
}

// catalogModel finds modelID ("<provider>/<model>") in the catalog; an
// empty id is the model opencode runs by default (first connected
// provider's default, as the model picker treats entry 0).
func catalogModel(cat *provider.OpencodeCatalog, modelID string) (provider.ModelSeed, bool) {
	if modelID == "" {
		ms := cat.Models()
		if len(ms) == 0 {
			return provider.ModelSeed{}, false
		}
		modelID = ms[0].ID
	}
	for _, p := range cat.Providers {
		for _, m := range p.Models {
			if m.ID == modelID {
				return m, true
			}
		}
	}
	return provider.ModelSeed{}, false
}
