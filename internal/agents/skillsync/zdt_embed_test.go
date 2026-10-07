package skillsync

import (
	"strings"
	"testing"
)

// A shipped skill is only shipped if it is in the embed FS — a new directory
// that nobody references is easy to add and easy to forget.
func TestZeroDowntimeUpgradeSkillIsShipped(t *testing.T) {
	names := BuiltinNames()
	if !names["wick-zero-downtime-upgrade"] {
		t.Fatalf("skill missing from the embedded set: %v", names)
	}
	b, err := builtinFS.ReadFile(builtinRoot + "/wick-zero-downtime-upgrade/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		"name: wick-zero-downtime-upgrade",
		"WICK_GRACEFUL_UPGRADE=1",
		"TimeoutStartSec=infinity",
		"WICK_DRAIN_QUIET",
		"WICK_DRAIN_TIMEOUT",
		// The command that installs the binary is the first thing an
		// operator needs; losing it from the skill leaves them on the
		// manual recipe with no preflight.
		"reload --binary",
		// A connector plugin is the other half of "shipping wick", and
		// the half that needs no reload at all. Without the build
		// recipe an agent reinvents it from the shell and rediscovers
		// the go.work trap every time, so pin both the command and the
		// two environment settings that make it work.
		"wick plugin build --kind connector",
		"plugin install",
		"GOWORK=off",
		"go mod edit -replace github.com/yogasw/wick=..",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md missing %q", want)
		}
	}
}
