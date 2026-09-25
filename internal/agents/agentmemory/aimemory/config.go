package aimemory

// Translating wick's Tuning into ai-memory's own configuration.
//
// ai-memory reads its settings from <data_dir>/config.toml, and wick never
// writes that file: an operator may have hand-edited and commented it, and a
// panel that rewrites a config it did not author destroys work it cannot see.
//
// Every key in that file is ALSO settable through an `AI_MEMORY_*` environment
// variable, with a DOUBLE UNDERSCORE standing in for a nested table — so
// `[decay] observation_retention_days` is `AI_MEMORY_DECAY__OBSERVATION_-
// RETENTION_DAYS`. Verified against ai-memory 2.4.0 on 2026-09-25 by giving
// each key below a wrongly-typed value and reading the key name back out of
// the config loader's startup error. So the settings ride the launch line as
// env, the file stays untouched, and clearing a field in the UI restores
// whatever the file says on the next start.
//
// The rule every entry follows: a ZERO VALUE IS NOT SENT. An unset field must
// mean "whatever the config file decided", never "zero" — the difference
// matters most for observation_retention_days, where 0 is itself a meaningful
// setting (never prune) that would then be forced on a file that said 90.

import (
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// tuningEnv renders the AI_MEMORY_* environment for one resolved Tuning.
func tuningEnv(t agentmemory.Tuning, authToken string) []string {
	var env []string
	set := func(key, val string) {
		if strings.TrimSpace(val) != "" {
			env = append(env, key+"="+val)
		}
	}
	setInt := func(key string, v int) {
		if v > 0 {
			env = append(env, key+"="+strconv.Itoa(v))
		}
	}
	setFloat := func(key string, v float64) {
		if v > 0 {
			env = append(env, key+"="+strconv.FormatFloat(v, 'f', -1, 64))
		}
	}
	// A bool is only sent when TRUE. Sending false would pin the setting off
	// against a config file that turned it on, which is the opposite of
	// "leave it alone" — and for capture_assistant, silently flipping the
	// safe direction is the one that matters.
	setTrue := func(key string, v bool) {
		if v {
			env = append(env, key+"=true")
		}
	}

	// Access & security.
	set("AI_MEMORY_LOG_LEVEL", t.LogLevel)
	if hosts := agentmemory.CSV(t.AllowedHosts); len(hosts) > 0 {
		set("AI_MEMORY_ALLOWED_HOSTS", strings.Join(hosts, ","))
	}
	// The token is the daemon's own bearer credential. It goes in env and
	// never in argv, because argv is logged and visible in a process list.
	set("AI_MEMORY_AUTH_TOKEN", authToken)

	// Capture & privacy. capture_mode, project_strategy and the hook half of
	// capture_assistant are NOT here: they are not config keys at all and
	// ride the generated hook line instead (see hookLine).
	setTrue("AI_MEMORY_CAPTURE_ASSISTANT", t.CaptureAssistant)
	setFloat("AI_MEMORY_HOOK_RATE_PER_SEC", t.HookRatePerSec)
	setFloat("AI_MEMORY_HOOK_RATE_BURST", t.HookRateBurst)
	if pats := agentmemory.Lines(t.SanitizeExtraPatterns); len(pats) > 0 {
		set("AI_MEMORY_SANITIZE__EXTRA_PATTERNS", strings.Join(pats, ","))
	}
	if allow := agentmemory.Lines(t.SanitizeAllowlist); len(allow) > 0 {
		set("AI_MEMORY_SANITIZE__ALLOWLIST", strings.Join(allow, ","))
	}

	// Model providers.
	set("AI_MEMORY_LLM_PROVIDER", t.LLMProvider)
	set("AI_MEMORY_LLM_MODEL", t.LLMModel)
	set("AI_MEMORY_EMBEDDING_PROVIDER", t.EmbeddingProvider)
	set("AI_MEMORY_EMBEDDING_MODEL", t.EmbeddingModel)
	setInt("AI_MEMORY_EMBEDDING_DIM", t.EmbeddingDim)
	setInt("AI_MEMORY_CONSOLIDATION__MAX_INPUT_TOKENS", t.MaxInputTokens)
	setInt("AI_MEMORY_CONSOLIDATION__MAX_OUTPUT_TOKENS", t.MaxOutputTokens)
	setTrue("AI_MEMORY_AUTO_IMPROVE__REQUIRE_APPROVAL", t.AutoImproveRequireApproval)
	setInt("AI_MEMORY_AUTO_IMPROVE__MIN_OBSERVATIONS", t.AutoImproveMinObservations)
	setFloat("AI_MEMORY_AUTO_IMPROVE__MIN_CONFIDENCE", t.AutoImproveMinConfidence)
	setInt("AI_MEMORY_AUTO_IMPROVE__MAX_PROPOSALS_PER_RUN", t.AutoImproveMaxProposals)

	// Retention. observation_retention_days is the one field where 0 is a
	// real setting and not an absence — but 0 is also the backend's own
	// default, so not sending it lands on the same behaviour either way.
	setInt("AI_MEMORY_DECAY__OBSERVATION_RETENTION_DAYS", t.ObservationRetentionDays)
	setInt("AI_MEMORY_DECAY__OBSERVATION_PRUNE_BATCH", t.ObservationPruneBatch)
	setInt("AI_MEMORY_DECAY__HARD_DELETE_AFTER_DAYS", t.HardDeleteAfterDays)
	setFloat("AI_MEMORY_DECAY__COLD_THRESHOLD", t.ColdThreshold)

	// Recall ranking.
	set("AI_MEMORY_RERANKER", t.Reranker)
	setFloat("AI_MEMORY_DECAY__LAMBDA", t.DecayLambda)
	setFloat("AI_MEMORY_DECAY__SIGMA", t.DecaySigma)
	setFloat("AI_MEMORY_DECAY__MU", t.DecayMu)
	setFloat("AI_MEMORY_DECAY__SALIENCE_DEFAULT", t.SalienceDefault)
	setFloat("AI_MEMORY_DECAY__BREADTH_WEIGHT", t.BreadthWeight)

	return env
}
