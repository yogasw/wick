package provider

// modelseed.go holds the ModelSeed type and an instance's effective model
// list. The catalog of default models per provider type (and its embedded +
// remote + disk-cache layering) lives in modelcatalog.go — this file only
// bridges the catalog to a single instance's configured Models.
//
// The CLIs can't enumerate their own models (no list command; `/model` only
// works in the interactive TUI), so the catalog seeds the well-known aliases
// each CLI's `--model` accepts and the operator can edit per instance (add a
// new model, drop one). Seeds are the aliases the CLIs document, not invented
// ids — e.g. `claude --model opus` is valid per `claude --help`.
//
// An instance's effective list is its own Models when set, else the catalog
// seed for its type. Empty seed (unknown type) → no picker.

// ModelSeed is one seeded model: the alias passed to --model plus a short
// human description shown in the picker (like the CLIs' own /model menu).
type ModelSeed struct {
	ID   string
	Desc string
}

// ModelEntry is one operator-curated model on an instance: the alias plus its
// description, both editable by hand in the model-selection kvlist. Persisted
// per-instance (userconfig), so an operator's description sticks even when the
// catalog changes. Empty Desc falls back to the catalog description at render.
type ModelEntry struct {
	ID   string `json:"id"`
	Desc string `json:"desc,omitempty"`
}

// EffectiveModels returns the models to offer for an instance: its curated
// Models when non-empty (each entry's own description, or the catalog's when
// the operator left it blank), otherwise the per-type catalog seed. Returns
// nil when the picker shouldn't be shown (ModelSelect off, or no seed).
func (i Instance) EffectiveModels() []ModelSeed {
	if !i.ModelSelect {
		return nil
	}
	if len(i.Models) > 0 {
		out := make([]ModelSeed, 0, len(i.Models))
		for _, m := range i.Models {
			desc := m.Desc
			if desc == "" {
				desc = seedDescFor(i.Type, m.ID)
			}
			out = append(out, ModelSeed{ID: m.ID, Desc: desc})
		}
		return out
	}
	return SeedModels(i.Type)
}
