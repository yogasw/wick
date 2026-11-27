package codex

import "github.com/yogasw/wick/internal/agents/provider"

// Codex takes `-c model_reasoning_effort=<id>` (see catalog.go); the
// spawner adds it from SpawnOptions.Effort. No default is claimed: it
// differs per model and the CLI does not report it.
func init() {
	provider.RegisterEffortResolver(provider.TypeCodex,
		provider.StaticEffortResolver("codex catalog", "", "minimal", "low", "medium", "high", "xhigh"))
}
