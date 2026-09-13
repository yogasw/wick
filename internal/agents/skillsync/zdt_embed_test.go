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
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md missing %q", want)
		}
	}
}
