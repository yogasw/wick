package api

import (
	"context"
	"strconv"
	"strings"

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
		MentionRouter:          def.SubAgentsMentionRouter,
		MaxIterations:          def.SubAgentsMaxIterations,
		MaxRuntimeMinutes:      def.SubAgentsMaxRuntimeMin,
		MinConfidence:          def.SubAgentsMinConfidence,
		NoNewEvidenceRounds:    def.SubAgentsNoEvidenceRounds,
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
	if v := p.cfg.GetOwned("agents", "sub_agents_mention_router"); v != "" {
		lim.MentionRouter = v == "true"
	}
	lim.MaxDepth = intOr(p.cfg.GetOwned("agents", "sub_agents_max_depth"), lim.MaxDepth)
	lim.RootBudget = intOr(p.cfg.GetOwned("agents", "sub_agents_root_budget"), lim.RootBudget)
	lim.MaxParallel = intOr(p.cfg.GetOwned("agents", "sub_agents_max_parallel"), lim.MaxParallel)
	lim.MaxTurnsCap = intOr(p.cfg.GetOwned("agents", "sub_agents_max_turns"), lim.MaxTurnsCap)
	lim.MaxHops = intOr(p.cfg.GetOwned("agents", "sub_agents_max_hops"), lim.MaxHops)
	lim.MaxIterations = intOr(p.cfg.GetOwned("agents", "sub_agents_max_iterations"), lim.MaxIterations)
	lim.MaxRuntimeMinutes = intOr(p.cfg.GetOwned("agents", "sub_agents_max_runtime_min"), lim.MaxRuntimeMinutes)
	lim.NoNewEvidenceRounds = intOr(p.cfg.GetOwned("agents", "sub_agents_no_evidence_rounds"), lim.NoNewEvidenceRounds)
	// Left unvalidated here on purpose: normalize() is the single place
	// that decides what an unrecognised confidence means, so the config
	// path and a hand-built Limits cannot disagree.
	if v := p.cfg.GetOwned("agents", "sub_agents_min_confidence"); v != "" {
		lim.MinConfidence = v
	}
	// Token ceilings accept an explicit 0 = "no cap", unlike the others:
	// providers that report no usage cannot be capped on spend at all, so
	// forcing a floor here would be a limit that silently never fires.
	lim.MaxTokensPerDelegation = intOrZero(p.cfg.GetOwned("agents", "sub_agents_max_tokens"), lim.MaxTokensPerDelegation)
	lim.RootTokenBudget = intOrZero(p.cfg.GetOwned("agents", "sub_agents_root_tokens"), lim.RootTokenBudget)
	return lim
}

// configSeedMarker records in the configs table that the investigation
// roles have been installed.
//
// A marker rather than plain insert-if-absent, because "fill in what is
// missing" and "a deleted role stays deleted" cannot both be true: a role
// an operator removed IS missing, and re-adding it every boot would make
// deletion impossible.
// The key is a field, not a constant, because more than one one-off boot
// step needs the same "has this already happened?" record.
type configSeedMarker struct {
	cfg *configs.Service
	key string
}

func (m configSeedMarker) Get() string {
	if m.cfg == nil {
		return ""
	}
	return m.cfg.GetOwned("agents", m.key)
}

func (m configSeedMarker) Set(value string) error {
	if m.cfg == nil {
		return nil
	}
	return m.cfg.SetOwned(context.Background(), "agents", m.key, value)
}

// secretValuesFor lists the values that must never reach a
// customer-facing draft.
//
// Every secret-marked config value, not only this project's: masking a
// value that does not appear in the draft costs nothing, while working
// out which secrets a given project could plausibly have leaked is
// guesswork that fails in the direction of leaking.
//
// Values shorter than the sanitiser's floor are skipped by it, so a
// trivially short "secret" cannot blank fragments of ordinary words.
func secretValuesFor(cfg *configs.Service) func(context.Context, string) []string {
	return func(context.Context, string) []string {
		if cfg == nil {
			return nil
		}
		rows := cfg.List()
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			if !row.IsSecret {
				continue
			}
			if v := strings.TrimSpace(cfg.GetOwned(row.Owner, row.Key)); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
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
