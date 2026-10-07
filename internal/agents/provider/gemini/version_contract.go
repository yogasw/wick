package gemini

import "github.com/yogasw/wick/internal/agents/provider/managedbin"

// init registers how `gemini --version` reads, so provider.Probe uses the
// same per-type version contract as the managed installer. gemini is not a
// wick-managed binary (no ReleaseSource), only probed.
func init() {
	managedbin.RegisterContract("gemini", managedbin.VersionContract{Args: []string{"--version"}, Parse: managedbin.FirstSemver})
}
