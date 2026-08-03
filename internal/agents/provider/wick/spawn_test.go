package wick

import (
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestPickModel_NoModels(t *testing.T) {
	if _, ok := pickModel(nil, ""); ok {
		t.Fatal("expected ok=false for nil instance")
	}
	inst := &provider.Instance{}
	if _, ok := pickModel(inst, ""); ok {
		t.Fatal("expected ok=false for empty WickModels")
	}
}

func TestPickModel_DefaultWins(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Default: false},
		{ID: "b", Model: "m-b", Default: true},
		{ID: "c", Model: "m-c", Default: false},
	}}
	m, ok := pickModel(inst, "")
	if !ok || m.ID != "b" {
		t.Fatalf("expected default model b, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_FirstEnabledWhenNoDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Disabled: true},
		{ID: "b", Model: "m-b"},
		{ID: "c", Model: "m-c"},
	}}
	m, ok := pickModel(inst, "")
	if !ok || m.ID != "b" {
		t.Fatalf("expected first enabled model b, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_DisabledDefaultSkipped(t *testing.T) {
	// A disabled model marked Default (shouldn't normally happen given the
	// handler-side invariant, but pickModel must not trust it blindly) —
	// falls back to the first enabled model instead.
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Default: true, Disabled: true},
		{ID: "b", Model: "m-b"},
	}}
	m, ok := pickModel(inst, "")
	if !ok || m.ID != "b" {
		t.Fatalf("expected fallback to enabled model b, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_AllDisabled(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Disabled: true},
		{ID: "b", Disabled: true},
	}}
	if _, ok := pickModel(inst, ""); ok {
		t.Fatal("expected ok=false when every model is disabled")
	}
}

func TestPickModel_PinnedModelWins(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Default: true},
		{ID: "b", Model: "m-b"},
	}}
	m, ok := pickModel(inst, "b")
	if !ok || m.ID != "b" {
		t.Fatalf("expected pinned model b to win over default a, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_LiveSetOverridesVendorModel(t *testing.T) {
	// A live set entry ("s", filter-based, no fixed Model) picked as
	// "<entryID>@<vendorID>" resolves the entry but overrides the model id.
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "gpt-5.2", Default: true},
		{ID: "s", Kind: "anthropic", BaseURL: "https://x/v1", DiscoveryFilter: "cc"},
	}}
	m, ok := pickModel(inst, "s@cc/claude-haiku-4-5")
	if !ok {
		t.Fatalf("expected live-set pick to resolve, ok=%v", ok)
	}
	if m.ID != "s" || m.Model != "cc/claude-haiku-4-5" || m.Kind != "anthropic" || m.BaseURL != "https://x/v1" {
		t.Fatalf("expected entry s with overridden model, got %+v", m)
	}
}

func TestPickModel_PinnedDisabledFallsBackToDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Default: true},
		{ID: "b", Model: "m-b", Disabled: true},
	}}
	m, ok := pickModel(inst, "b")
	if !ok || m.ID != "a" {
		t.Fatalf("expected fallback to default a when pin is disabled, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_PinnedUnknownIDFallsBackToDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Model: "m-a", Default: true},
	}}
	m, ok := pickModel(inst, "does-not-exist")
	if !ok || m.ID != "a" {
		t.Fatalf("expected fallback to default a for unknown pin, got %+v ok=%v", m, ok)
	}
}

// A live set has no Model of its own. Picked WITHOUT a pin — which is
// every sub-agent whose role names no model — it has to resolve to its
// sticky default, or the spawn reaches the vendor with an empty model id
// and dies there with "model is empty".
func TestPickModel_LiveSetDefaultResolvesWithoutAPin(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "s", Kind: "google", LiveSet: true, DefaultVendorModel: "gemini-3-pro", Default: true},
	}}
	m, ok := pickModel(inst, "")
	if !ok {
		t.Fatal("expected the live-set default to resolve")
	}
	if m.Model != "gemini-3-pro" {
		t.Fatalf("model = %q, want the sticky default vendor model", m.Model)
	}
}

// A live set nobody has picked a default for cannot be called: skip it and
// use something that can, rather than spawning with no model id.
func TestPickModel_UnresolvableLiveSetIsSkipped(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "s", LiveSet: true, Default: true},
		{ID: "a", Model: "m-a"},
	}}
	m, ok := pickModel(inst, "")
	if !ok || m.ID != "a" {
		t.Fatalf("got %+v ok=%v, want the runnable entry a", m, ok)
	}
}

// Nothing runnable at all is a configuration problem, and the operator
// gets told so — better than a vendor SDK error nobody can act on.
func TestPickModel_NoRunnableModel(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "s", LiveSet: true},
		{ID: "b", Model: "   "},
	}}
	if m, ok := pickModel(inst, "s"); ok {
		t.Fatalf("got %+v, want ok=false when no entry resolves to a model id", m)
	}
}
