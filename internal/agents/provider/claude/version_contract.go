package claude

import "github.com/yogasw/wick/internal/agents/provider/managedbin"

// init registers how `claude --version` reads, so provider.Probe uses the
// same per-type version contract as the managed installer. claude is not a
// wick-managed binary (no ReleaseSource), only probed.
func init() {
	managedbin.RegisterContract("claude", managedbin.VersionContract{Args: []string{"--version"}, Parse: managedbin.FirstSemver})
}
