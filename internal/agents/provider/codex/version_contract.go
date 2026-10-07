package codex

import "github.com/yogasw/wick/internal/agents/provider/managedbin"

// init registers how `codex --version` reads, so provider.Probe uses the
// same per-type version contract as the managed installer. codex is not a
// wick-managed binary (no ReleaseSource), only probed.
func init() {
	managedbin.RegisterContract("codex", managedbin.VersionContract{Args: []string{"--version"}, Parse: managedbin.FirstSemver})
}
