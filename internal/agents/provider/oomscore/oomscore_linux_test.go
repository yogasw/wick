//go:build linux || android

package oomscore

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Adjust writes the score where the kernel reads it. The test points the
// package at a temp root so it never touches a real process.
func TestAdjust_WritesScore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "4412"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := setProcRoot(root)
	defer restore()

	if err := Adjust(4412, AgentScore); err != nil {
		t.Fatalf("Adjust: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "4412", "oom_score_adj"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "800" {
		t.Fatalf("score = %q, want %q", got, "800")
	}
}

// A process that already exited must not turn into a spawn failure — the
// guard is advisory, and the agent is already running by the time we set it.
func TestAdjust_MissingProcessIsError(t *testing.T) {
	root := t.TempDir()
	restore := setProcRoot(root)
	defer restore()

	if err := Adjust(9999, AgentScore); err == nil {
		t.Fatal("Adjust on a missing pid returned nil, want an error the caller can log and ignore")
	}
}

// Out-of-range scores are a programming error, not something to pass to
// the kernel: the valid range is -1000..1000.
func TestAdjust_RejectsOutOfRange(t *testing.T) {
	root := t.TempDir()
	restore := setProcRoot(root)
	defer restore()

	for _, score := range []int{-1001, 1001} {
		if err := Adjust(1, score); err == nil {
			t.Fatalf("score %d accepted, want rejection", score)
		}
	}
}

// Protecting wick is half the job: biasing agents UP the victim list does
// nothing if the daemon itself stays an ordinary candidate. Once the
// agents are gone and pressure remains, the OOM killer would pick the
// process whose survival is the entire point of the feature.
func TestAdjustSelf_LowersOwnScore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, strconv.Itoa(os.Getpid())), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := setProcRoot(root)
	defer restore()

	if err := AdjustSelf(DaemonScore); err != nil {
		t.Fatalf("AdjustSelf: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, strconv.Itoa(os.Getpid()), "oom_score_adj"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != strconv.Itoa(DaemonScore) {
		t.Fatalf("own score = %q, want %d", got, DaemonScore)
	}
}
