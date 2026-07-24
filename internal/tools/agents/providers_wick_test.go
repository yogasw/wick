package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// TestNormalizeDefault covers the single-default invariant across the
// disabled-model edge cases added alongside per-model Disable/Enable:
// a disabled model must never end up Default, whether because it was
// explicitly saved as default or because it was the only fallback
// candidate.
func TestNormalizeDefault(t *testing.T) {
	t.Run("explicit default wins and clears others", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a", Default: true},
			{ID: "b"},
		}}
		normalizeDefault(ins, "b", true)
		if !ins.WickModels[1].Default || ins.WickModels[0].Default {
			t.Fatalf("want only b default, got %+v", ins.WickModels)
		}
	})

	t.Run("nothing default picks the saved model", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a"},
			{ID: "b"},
		}}
		normalizeDefault(ins, "b", false)
		if !ins.WickModels[1].Default {
			t.Fatalf("want b default, got %+v", ins.WickModels)
		}
	})

	t.Run("an existing enabled default is left alone", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a", Default: true},
			{ID: "b"},
		}}
		normalizeDefault(ins, "b", false)
		if !ins.WickModels[0].Default || ins.WickModels[1].Default {
			t.Fatalf("want a to remain default, got %+v", ins.WickModels)
		}
	})

	t.Run("a disabled existing default does not block re-election", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a", Default: true, Disabled: true},
			{ID: "b"},
		}}
		normalizeDefault(ins, "b", false)
		if !ins.WickModels[1].Default {
			t.Fatalf("want b to become default over a disabled default, got %+v", ins.WickModels)
		}
	})

	t.Run("saving a disabled model as default falls back to the first enabled model", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a", Disabled: true},
			{ID: "b"},
		}}
		// savedID "a" is disabled — normalizeDefault must not make it default.
		normalizeDefault(ins, "a", false)
		if ins.WickModels[0].Default {
			t.Fatalf("disabled model a must never become default, got %+v", ins.WickModels)
		}
		if !ins.WickModels[1].Default {
			t.Fatalf("want fallback to enabled model b, got %+v", ins.WickModels)
		}
	})

	t.Run("all disabled leaves nothing default", func(t *testing.T) {
		ins := &provider.Instance{WickModels: []provider.WickModel{
			{ID: "a", Disabled: true},
			{ID: "b", Disabled: true},
		}}
		normalizeDefault(ins, "a", false)
		for _, m := range ins.WickModels {
			if m.Default {
				t.Fatalf("expected no default when every model is disabled, got %+v", ins.WickModels)
			}
		}
	})
}
