package wick

import (
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func f64(v float64) *float64 { return &v }
func iptr(v int) *int        { return &v }

// TestBuildGenConfig_ModelOverridesInstance verifies the merge order:
// model GenConfig wins over instance GenConfig.
func TestBuildGenConfig_ModelOverridesInstance(t *testing.T) {
	inst := &provider.WickConfig{GenConfig: &provider.WickGenConfig{Temperature: f64(0.2)}}
	m := provider.WickModel{GenConfig: &provider.WickGenConfig{Temperature: f64(0.9)}}
	cfg := buildGenConfig(m, inst)
	if cfg.Temperature == nil || *cfg.Temperature != 0.9 {
		t.Fatalf("model temperature should win, got %v", cfg.Temperature)
	}
}

// TestBuildGenConfig_RawThenStructured: structured fields override raw
// JSON (a UI toggle always beats stale raw config).
func TestBuildGenConfig_RawThenStructured(t *testing.T) {
	m := provider.WickModel{
		RawConfig: `{"temperature":0.1,"topP":0.5}`,
		GenConfig: &provider.WickGenConfig{Temperature: f64(0.7)},
	}
	cfg := buildGenConfig(m, nil)
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Fatalf("structured temperature should override raw, got %v", cfg.Temperature)
	}
	// topP came only from raw — it should survive.
	if cfg.TopP == nil || *cfg.TopP != 0.5 {
		t.Fatalf("raw topP should persist, got %v", cfg.TopP)
	}
}

// TestBuildGenConfig_Thinking maps a thinking budget into ThinkingConfig,
// enabling thoughts when the budget is non-zero.
func TestBuildGenConfig_Thinking(t *testing.T) {
	m := provider.WickModel{GenConfig: &provider.WickGenConfig{ThinkingBudget: iptr(2048)}}
	cfg := buildGenConfig(m, nil)
	if cfg.ThinkingConfig == nil || !cfg.ThinkingConfig.IncludeThoughts {
		t.Fatalf("thinking should be enabled, got %+v", cfg.ThinkingConfig)
	}
	if cfg.ThinkingConfig.ThinkingBudget == nil || *cfg.ThinkingConfig.ThinkingBudget != 2048 {
		t.Fatalf("thinking budget wrong: %+v", cfg.ThinkingConfig)
	}
	// Budget 0 disables thoughts.
	m0 := provider.WickModel{GenConfig: &provider.WickGenConfig{ThinkingBudget: iptr(0)}}
	cfg0 := buildGenConfig(m0, nil)
	if cfg0.ThinkingConfig == nil || cfg0.ThinkingConfig.IncludeThoughts {
		t.Fatalf("budget 0 should disable thoughts, got %+v", cfg0.ThinkingConfig)
	}
}

// TestBuildGenConfig_ModelMaxOutputTokens: the per-model MaxOutputTokens
// column is the most explicit output cap.
func TestBuildGenConfig_ModelMaxOutputTokens(t *testing.T) {
	m := provider.WickModel{MaxOutputTokens: 8192}
	cfg := buildGenConfig(m, nil)
	if cfg.MaxOutputTokens != 8192 {
		t.Fatalf("want 8192, got %d", cfg.MaxOutputTokens)
	}
}

// TestBuildGenConfig_InvalidRawIgnored: a bad raw JSON string is ignored
// (fail-soft) rather than bricking the spawn.
func TestBuildGenConfig_InvalidRawIgnored(t *testing.T) {
	m := provider.WickModel{RawConfig: `{not valid json`}
	cfg := buildGenConfig(m, nil) // must not panic
	if cfg == nil {
		t.Fatal("nil config from invalid raw")
	}
}
