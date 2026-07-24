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
		{ID: "a", Default: false},
		{ID: "b", Default: true},
		{ID: "c", Default: false},
	}}
	m, ok := pickModel(inst, "")
	if !ok || m.ID != "b" {
		t.Fatalf("expected default model b, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_FirstEnabledWhenNoDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Disabled: true},
		{ID: "b"},
		{ID: "c"},
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
		{ID: "a", Default: true, Disabled: true},
		{ID: "b"},
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
		{ID: "a", Default: true},
		{ID: "b"},
	}}
	m, ok := pickModel(inst, "b")
	if !ok || m.ID != "b" {
		t.Fatalf("expected pinned model b to win over default a, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_PinnedDisabledFallsBackToDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Default: true},
		{ID: "b", Disabled: true},
	}}
	m, ok := pickModel(inst, "b")
	if !ok || m.ID != "a" {
		t.Fatalf("expected fallback to default a when pin is disabled, got %+v ok=%v", m, ok)
	}
}

func TestPickModel_PinnedUnknownIDFallsBackToDefault(t *testing.T) {
	inst := &provider.Instance{WickModels: []provider.WickModel{
		{ID: "a", Default: true},
	}}
	m, ok := pickModel(inst, "does-not-exist")
	if !ok || m.ID != "a" {
		t.Fatalf("expected fallback to default a for unknown pin, got %+v ok=%v", m, ok)
	}
}
