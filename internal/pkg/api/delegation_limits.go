package api

import (
	"strconv"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/configs"
)

// delegationLimitsProvider reads the sub-agent governor ceilings from the
// configs table on EVERY delegation rather than caching them at boot.
//
// That matters for the kill-switch in particular: an operator flipping
// "Sub-agents enabled" off during an incident must stop the next spawn
// immediately, not at the next restart.
type delegationLimitsProvider struct {
	cfg *configs.Service
}

// delegationLimits returns a Limits value that re-reads configuration
// each time the governor consults it.
func delegationLimits(cfg *configs.Service) delegation.Limits {
	p := delegationLimitsProvider{cfg: cfg}
	return p.current()
}

// current snapshots the live values, falling back to the packaged
// defaults for any key that is absent or unparseable. A malformed row
// must never read as "unlimited".
func (p delegationLimitsProvider) current() delegation.Limits {
	def := config.DefaultGeneralConfig()
	lim := delegation.Limits{
		MaxDepth:               def.SubAgentsMaxDepth,
		RootBudget:             def.SubAgentsRootBudget,
		MaxParallel:            def.SubAgentsMaxParallel,
		MaxTurnsCap:            def.SubAgentsMaxTurns,
		MaxTokensPerDelegation: def.SubAgentsMaxTokens,
		RootTokenBudget:        def.SubAgentsRootTokens,
		MaxHops:                def.SubAgentsMaxHops,
		Disabled:               !def.SubAgentsEnabled,
	}
	if p.cfg == nil {
		return lim
	}
	// Keys are the snake_case names StructToConfigs derives from the
	// GeneralConfig field names — they must stay in step with it.
	if v := p.cfg.GetOwned("agents", "sub_agents_enabled"); v != "" {
		lim.Disabled = v != "true"
	}
	lim.MaxDepth = intOr(p.cfg.GetOwned("agents", "sub_agents_max_depth"), lim.MaxDepth)
	lim.RootBudget = intOr(p.cfg.GetOwned("agents", "sub_agents_root_budget"), lim.RootBudget)
	lim.MaxParallel = intOr(p.cfg.GetOwned("agents", "sub_agents_max_parallel"), lim.MaxParallel)
	lim.MaxTurnsCap = intOr(p.cfg.GetOwned("agents", "sub_agents_max_turns"), lim.MaxTurnsCap)
	lim.MaxHops = intOr(p.cfg.GetOwned("agents", "sub_agents_max_hops"), lim.MaxHops)
	// Token ceilings accept an explicit 0 = "no cap", unlike the others:
	// providers that report no usage cannot be capped on spend at all, so
	// forcing a floor here would be a limit that silently never fires.
	lim.MaxTokensPerDelegation = intOrZero(p.cfg.GetOwned("agents", "sub_agents_max_tokens"), lim.MaxTokensPerDelegation)
	lim.RootTokenBudget = intOrZero(p.cfg.GetOwned("agents", "sub_agents_root_tokens"), lim.RootTokenBudget)
	return lim
}

// intOrZero parses a config value that may legitimately be 0.
func intOrZero(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// intOr parses a config value, keeping the fallback when the row is
// missing, non-numeric, or non-positive.
func intOr(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
