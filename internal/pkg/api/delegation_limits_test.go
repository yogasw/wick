package api

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/pkg/entity"
)

// The config keys read at runtime must match the snake_case names
// StructToConfigs derives from the GeneralConfig field names. A typo here
// is silent: every knob keeps its default and an operator's setting is
// ignored with no error anywhere.
func TestGovernorConfigKeysMatchStructFields(t *testing.T) {
	configs := entity.StructToConfigs(config.GeneralConfig{})
	have := map[string]bool{}
	for _, c := range configs {
		have[c.Key] = true
	}

	for _, key := range []string{
		"sub_agents_enabled",
		"sub_agents_max_depth",
		"sub_agents_root_budget",
		"sub_agents_max_parallel",
		"sub_agents_max_turns",
		"sub_agents_max_tokens",
		"sub_agents_root_tokens",
		"sub_agents_stale_claim_min",
		"sub_agents_mention_router",
		"sub_agents_max_iterations",
		"sub_agents_max_runtime_min",
		"sub_agents_min_confidence",
		"sub_agents_no_evidence_rounds",
	} {
		if !have[key] {
			t.Fatalf("config key %q is read at runtime but no GeneralConfig field produces it", key)
		}
	}
}

// The mention router defaults ON and is turned off only by an explicit
// "false". Anything else — an absent row, a typo — must leave the feature
// as shipped rather than silently disabling it.
func TestMentionRouterDefaultsOnAndIsExplicitlyDisabled(t *testing.T) {
	if !config.DefaultGeneralConfig().SubAgentsMentionRouter {
		t.Fatal("the mention router must ship on")
	}

	p := delegationLimitsProvider{}
	if !p.current().MentionRouter {
		t.Fatal("with no config service the packaged default must win")
	}
}

// A missing or malformed row must fall back to the default, never to
// zero — zero on a brake means "unlimited".
func TestIntOrFallsBackOnJunk(t *testing.T) {
	for _, raw := range []string{"", "abc", "-5", "0"} {
		if got := intOr(raw, 7); got != 7 {
			t.Fatalf("raw %q gave %d, want the fallback 7", raw, got)
		}
	}
	if got := intOr("12", 7); got != 12 {
		t.Fatalf("got %d, want 12", got)
	}
}

// Token ceilings are different: 0 legitimately means "no cap", because a
// provider that reports no usage cannot be capped on spend at all.
func TestIntOrZeroAcceptsExplicitZero(t *testing.T) {
	if got := intOrZero("0", 500); got != 0 {
		t.Fatalf("got %d; an explicit 0 must disable the token cap", got)
	}
	if got := intOrZero("abc", 500); got != 500 {
		t.Fatalf("got %d, want the fallback on junk", got)
	}
}

// Sub-agents must remain opt-in: the feature spawns processes and spends
// money, so a fresh install must not discover it by surprise.
func TestSubAgentsShipDisabled(t *testing.T) {
	if config.DefaultGeneralConfig().SubAgentsEnabled {
		t.Fatal("sub-agent delegation must default to off")
	}
}
