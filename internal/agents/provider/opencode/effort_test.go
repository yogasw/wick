package opencode

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestBuildArgsVariant(t *testing.T) {
	got := buildArgs(provider.SpawnOptions{Effort: "high"}, nil, "acme/model-a", false)
	i := slices.Index(got, "--variant")
	if i < 0 || got[i+1] != "high" {
		t.Fatalf("want --variant high, got %q", got)
	}
	if slices.Contains(buildArgs(provider.SpawnOptions{}, nil, "", false), "--variant") {
		t.Fatal("no effort must add no --variant")
	}
}

func TestVariantNamesOrder(t *testing.T) {
	v := map[string]json.RawMessage{"high": nil, "custom": nil, "low": nil, "max": nil, "medium": nil}
	if got := variantNames(v); !slices.Equal(got, []string{"low", "medium", "high", "max", "custom"}) {
		t.Fatalf("got %v", got)
	}
	if variantNames(nil) != nil {
		t.Fatal("no variants must stay nil")
	}
}

func TestCatalogModelVariants(t *testing.T) {
	cat := &provider.OpencodeCatalog{
		Connected: []string{"acme"},
		Default:   map[string]string{"acme": "model-a"},
		Providers: []provider.OpencodeCatalogProvider{{ID: "acme", Models: []provider.ModelSeed{
			{ID: "acme/model-a", Variants: []string{"low", "high"}},
			{ID: "acme/model-b"},
		}}},
	}
	m, ok := catalogModel(cat, "acme/model-a")
	if !ok || !slices.Equal(m.Variants, []string{"low", "high"}) {
		t.Fatalf("model-a: %+v ok=%v", m, ok)
	}
	if m, ok := catalogModel(cat, "acme/model-b"); !ok || len(m.Variants) != 0 {
		t.Fatalf("model-b must have no variants: %+v", m)
	}
	if m, ok := catalogModel(cat, ""); !ok || m.ID != "acme/model-a" {
		t.Fatalf("empty id must be the default model, got %+v", m)
	}
	if _, ok := catalogModel(cat, "acme/missing"); ok {
		t.Fatal("unknown model must not resolve")
	}
}
