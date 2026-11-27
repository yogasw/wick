package wick

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestWickEffortResolverDefault(t *testing.T) {
	ins := provider.Instance{
		Type:       provider.TypeWick,
		Name:       "acme",
		WickConfig: &provider.WickConfig{GenConfig: &provider.WickGenConfig{ReasoningEffort: "low"}},
		WickModels: []provider.WickModel{
			{ID: "m1", Kind: "openai", Model: "model-a", APIKey: "k", Default: true},
			{ID: "m2", Kind: "openai", Model: "model-b", APIKey: "k", GenConfig: &provider.WickGenConfig{ReasoningEffort: "High"}},
		},
	}
	caps, err := resolveEffort(context.Background(), ins, "m1")
	if err != nil || len(caps.Options) != 3 || caps.Default != "low" {
		t.Fatalf("m1 inherits the instance effort: %+v err=%v", caps, err)
	}
	caps, _ = resolveEffort(context.Background(), ins, "m2")
	if caps.Default != "high" {
		t.Fatalf("m2's own effort wins: %+v", caps)
	}
}

func TestPickedEffortWinsOverBaseline(t *testing.T) {
	e := &engine{}
	e.setReasoning(&ReasoningConfig{Effort: "low"})
	e.setPickedEffort("high")
	if r := e.effectiveReasoning(); r == nil || r.Effort != "high" {
		t.Fatalf("picked effort must win, got %+v", r)
	}
	e.setPickedEffort("")
	if r := e.effectiveReasoning(); r == nil || r.Effort != "low" {
		t.Fatalf("no pick keeps the baseline, got %+v", r)
	}
}
