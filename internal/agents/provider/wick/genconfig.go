package wick

import (
	"encoding/json"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// buildGenConfig assembles the genai generation config for a spawn,
// applying the plan's merge order (least→most specific):
//
//	adk defaults ← instance RawConfig ← instance GenConfig
//	            ← model RawConfig    ← model GenConfig
//
// RawConfig is the escape hatch: any adk-go/genai option without a
// structured field is set by unmarshalling the raw JSON straight into
// the genai.GenerateContentConfig. Structured fields then override so a
// UI toggle always wins over stale raw JSON.
func buildGenConfig(m provider.WickModel, inst *provider.WickConfig) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{}

	if inst != nil {
		applyRawConfig(cfg, inst.RawConfig)
		if inst.GenConfig != nil {
			applyGenConfig(cfg, inst.GenConfig)
		}
	}
	applyRawConfig(cfg, m.RawConfig)
	if m.GenConfig != nil {
		applyGenConfig(cfg, m.GenConfig)
	}
	// A per-model MaxOutputTokens set on the model row (not the gen
	// config) is the most explicit output cap.
	if m.MaxOutputTokens > 0 {
		cfg.MaxOutputTokens = int32(m.MaxOutputTokens)
	}
	return cfg
}

// applyRawConfig unmarshals raw JSON over the config in place. Invalid
// JSON is logged and ignored (fail-soft — a typo in the escape hatch
// must not brick the spawn).
func applyRawConfig(cfg *genai.GenerateContentConfig, raw string) {
	if raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		log.Warn().Err(err).Msg("wick.genconfig: invalid raw ADK config JSON, ignoring")
	}
}

// applyGenConfig applies the structured knobs, overriding whatever raw
// config set. Pointers distinguish "unset" from zero.
func applyGenConfig(cfg *genai.GenerateContentConfig, g *provider.WickGenConfig) {
	if g.Temperature != nil {
		t := float32(*g.Temperature)
		cfg.Temperature = &t
	}
	if g.TopP != nil {
		p := float32(*g.TopP)
		cfg.TopP = &p
	}
	if g.MaxOutputTokens > 0 {
		cfg.MaxOutputTokens = int32(g.MaxOutputTokens)
	}
	if g.ThinkingBudget != nil {
		budget := int32(*g.ThinkingBudget)
		include := budget != 0
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: include,
			ThinkingBudget:  &budget,
		}
	}
}
