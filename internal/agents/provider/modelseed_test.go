package provider

import (
	"slices"
	"testing"
)

func modelIDs(seeds []ModelSeed) []string {
	out := make([]string, len(seeds))
	for i, s := range seeds {
		out[i] = s.ID
	}
	return out
}

func TestEffectiveModels(t *testing.T) {
	// Isolate the disk cache + rebuild so this asserts against the embedded
	// baseline, not whatever remote data a dev machine has cached.
	isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	// ModelSelect off → no picker regardless of Models.
	off := Instance{Type: TypeClaude, ModelSelect: false, Models: []ModelEntry{{ID: "opus"}}}
	if got := off.EffectiveModels(); got != nil {
		t.Errorf("ModelSelect off should yield nil, got %v", got)
	}

	// On + empty Models → per-type seed (same ids as SeedModels).
	seed := Instance{Type: TypeClaude, ModelSelect: true}
	if got := seed.EffectiveModels(); !slices.Equal(modelIDs(got), modelIDs(SeedModels(TypeClaude))) {
		t.Errorf("empty Models should fall back to seed, got %v", got)
	}
	// Seed models carry descriptions.
	if got := seed.EffectiveModels(); len(got) == 0 || got[0].Desc == "" {
		t.Errorf("seed models should carry a description, got %+v", got)
	}

	// On + curated Models → those ids, not the seed. A known id with a blank
	// desc inherits the catalog description; an unknown one has none; an
	// explicit per-instance desc is kept verbatim.
	cur := Instance{Type: TypeClaude, ModelSelect: true, Models: []ModelEntry{
		{ID: "opus"},                          // blank desc → catalog fills it
		{ID: "my-custom"},                     // unknown → no desc
		{ID: "sonnet", Desc: "my own words"},  // explicit desc → kept
	}}
	got := cur.EffectiveModels()
	if !slices.Equal(modelIDs(got), []string{"opus", "my-custom", "sonnet"}) {
		t.Errorf("curated Models should win, got %v", got)
	}
	if got[0].Desc == "" {
		t.Error("known curated model 'opus' should inherit its catalog description")
	}
	if got[1].Desc != "" {
		t.Errorf("unknown curated model should have no description, got %q", got[1].Desc)
	}
	if got[2].Desc != "my own words" {
		t.Errorf("explicit per-instance desc should be kept, got %q", got[2].Desc)
	}

	// Unknown type + on + empty → no seed → nil.
	unk := Instance{Type: Type("weird"), ModelSelect: true}
	if got := unk.EffectiveModels(); got != nil {
		t.Errorf("unknown type with no seed should yield nil, got %v", got)
	}
}

func TestModelArgs(t *testing.T) {
	inst := &Instance{Type: TypeClaude}

	// No model pinned → nil.
	if a := ModelArgs(SpawnOptions{Instance: inst}, nil); a != nil {
		t.Errorf("no ModelID should yield nil, got %v", a)
	}

	// Pinned → --model.
	a := ModelArgs(SpawnOptions{Instance: inst, ModelID: "opus"}, nil)
	if !slices.Equal(a, []string{"--model", "opus"}) {
		t.Errorf("want [--model opus], got %v", a)
	}

	// AI-router → skip (router sets model via env).
	air := &Instance{Type: TypeClaude, UseAIRouter: true}
	if a := ModelArgs(SpawnOptions{Instance: air, ModelID: "opus"}, nil); a != nil {
		t.Errorf("AI-router should skip --model, got %v", a)
	}

	// Already has --model in existing args → don't double.
	existing := []string{"--model", "sonnet"}
	if a := ModelArgs(SpawnOptions{Instance: inst, ModelID: "opus"}, existing); a != nil {
		t.Errorf("existing --model should suppress, got %v", a)
	}
}

// TestSeedModels lives in modelcatalog_test.go now (embedded-baseline case)
// since the seed list is sourced from the catalog rather than a static map.
