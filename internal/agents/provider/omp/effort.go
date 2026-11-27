package omp

import "github.com/yogasw/wick/internal/agents/provider"

// omp takes `--thinking <level>` (see catalog.go). "off" is an explicit
// level, not Default; "auto" lets omp decide per turn.
func init() {
	provider.RegisterEffortResolver(provider.TypeOMP,
		provider.StaticEffortResolver("omp catalog", "", "off", "minimal", "low", "medium", "high", "xhigh", "max", "auto"))
}
