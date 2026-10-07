package agents

import (
	"context"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// wick is the first registered ModelSets and must behave as the old
// `if TypeWick` branch: registered models (live sets as expandable rows),
// one-segment paths, and legacy "<entry>@<model>" pins resolving unchanged.
func TestWickModelSetsCompat(t *testing.T) {
	s, ok := provider.ModelSetsFor(provider.TypeWick)
	if !ok {
		t.Fatal("wick ModelSets not registered")
	}
	ins := provider.Instance{Type: provider.TypeWick, Name: "x", WickModels: []provider.WickModel{
		{ID: "m1", Label: "Fixed", Model: "gpt-x"},
		{ID: "set1", LiveSet: true, Default: true},
		{ID: "off", LiveSet: true, Disabled: true},
	}}
	rows, _ := s.Sets(context.Background(), ins)
	var got []provider.ModelChoice
	for _, r := range rows {
		got = append(got, provider.ModelChoice{ID: r.ID, Live: r.Live, Default: r.Default})
	}
	want := []provider.ModelChoice{{ID: "set1", Live: true, Default: true}, {ID: "m1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sets = %+v", got)
	}
	if rows, _ := s.Expand(context.Background(), ins, []string{"a", "b"}); rows != nil {
		t.Fatal("wick sets are one level deep")
	}
	path, model, grouped := provider.DecodePin("set1@models/gemini-3-pro")
	sp, err := s.Resolve(ins, path, model)
	if !grouped || err != nil || sp.Model != "models/gemini-3-pro" || sp.Provider != "set1" {
		t.Fatalf("legacy pin: %+v %v", sp, err)
	}
	if _, err := s.Resolve(ins, []string{"off"}, "x"); err == nil {
		t.Fatal("disabled set must not resolve")
	}
}
