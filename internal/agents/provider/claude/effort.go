package claude

import "github.com/yogasw/wick/internal/agents/provider"

// Claude Code reads its effort level from CLAUDE_CODE_EFFORT_LEVEL (see
// catalog.go); the spawner sets it from SpawnOptions.Effort. The CLI does
// not report a default, so none is claimed.
func init() {
	provider.RegisterEffortResolver(provider.TypeClaude,
		provider.StaticEffortResolver("claude catalog", "", "low", "medium", "high", "xhigh"))
}
