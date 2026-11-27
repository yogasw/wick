package wick

import (
	"context"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// wickEffortLevels are the vendor-agnostic levels the adapters map to each
// API (OpenAI/OpenRouter reasoning.effort, Anthropic budget, Gemini level).
var wickEffortLevels = []string{"low", "medium", "high"}

func init() {
	provider.RegisterEffortResolver(provider.TypeWick, resolveEffort)
}

// resolveEffort offers the wick levels for a runnable model; its default is
// the reasoning effort configured on the model (else the instance), so
// Default shows what a turn really runs with.
func resolveEffort(_ context.Context, ins provider.Instance, modelID string) (provider.EffortCapabilities, error) {
	m, ok := pickModel(&ins, modelID)
	if !ok {
		return provider.EffortCapabilities{Source: "wick"}, nil
	}
	caps := provider.EffortCapabilities{Source: "wick"}
	for _, id := range wickEffortLevels {
		caps.Options = append(caps.Options, provider.EffortOption{ID: id, Label: provider.EffortLabel(id)})
	}
	def := ""
	if ins.WickConfig != nil && ins.WickConfig.GenConfig != nil {
		def = strings.ToLower(strings.TrimSpace(ins.WickConfig.GenConfig.ReasoningEffort))
	}
	if m.GenConfig != nil {
		if e := strings.ToLower(strings.TrimSpace(m.GenConfig.ReasoningEffort)); e != "" {
			def = e
		}
	}
	for _, id := range wickEffortLevels {
		if id == def {
			caps.Default = def
		}
	}
	return caps, nil
}
