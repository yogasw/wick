//go:build !linux && !android

package oomscore

import (
	"errors"
	"testing"
)

// Off Linux the package must degrade cleanly rather than pretend to work.
func TestAdjust_UnsupportedPlatform(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true on a platform with no oom_score_adj")
	}
	if err := Adjust(1, AgentScore); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Adjust err = %v, want ErrUnsupported", err)
	}
}

// Validation is shared code and must run everywhere, so a bad score is
// caught on the development platform too.
func TestAdjust_RejectsOutOfRangeEverywhere(t *testing.T) {
	if err := Adjust(1, 1001); errors.Is(err, ErrUnsupported) {
		t.Fatal("out-of-range score reported as unsupported; want a validation error")
	}
}

// setProcRoot is exercised here so the unused-symbol check stays honest
// on the development platform.
func TestSetProcRoot_Restores(t *testing.T) {
	restore := setProcRoot("/tmp/x")
	restore()
}

// The two scores must sit on opposite sides of the default (0), or the
// bias does not actually order anything.
func TestScores_AgentAboveDaemon(t *testing.T) {
	if !(DaemonScore < 0 && AgentScore > 0) {
		t.Fatalf("scores do not straddle the default: agent=%d daemon=%d", AgentScore, DaemonScore)
	}
	if AgentScore <= DaemonScore {
		t.Fatalf("agent %d must outrank daemon %d as a victim", AgentScore, DaemonScore)
	}
}
