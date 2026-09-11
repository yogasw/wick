package api

import (
	"testing"

	"github.com/yogasw/wick/internal/pkg/daemon"
)

func pend(version string, size, mod int64) daemon.Pending {
	return daemon.Pending{Path: "/usr/bin/app", Version: version, Size: size, ModTime: mod}
}

// TestAutoSwapDecision locks the guards on a process replacing itself without
// anybody asking. Each one exists because of a way this could go wrong.
func TestAutoSwapDecision(t *testing.T) {
	t.Run("a newly seen binary is not swapped to immediately", func(t *testing.T) {
		// A file that appeared between two ticks may still be being copied.
		// Executing half a binary is the one failure with no recovery path.
		var a autoSwapper
		if a.shouldSwap(pend("0.2.0", 100, 10), true, nil) {
			t.Fatal("swapped on first sighting")
		}
	})

	t.Run("swaps once the file has held still and nothing is running", func(t *testing.T) {
		var a autoSwapper
		p := pend("0.2.0", 100, 10)
		a.shouldSwap(p, true, nil)
		if !a.shouldSwap(p, true, nil) {
			t.Fatal("did not swap to a stable binary on an idle process")
		}
	})

	t.Run("a file still changing restarts the wait", func(t *testing.T) {
		var a autoSwapper
		a.shouldSwap(pend("0.2.0", 100, 10), true, nil)
		if a.shouldSwap(pend("0.2.0", 180, 11), true, nil) {
			t.Fatal("swapped to a binary that was still growing")
		}
	})

	t.Run("work in flight holds the swap", func(t *testing.T) {
		// Not because the handover would interrupt it — it would not — but
		// because two processes alive for the length of a long agent turn is
		// a state nobody chose.
		var a autoSwapper
		p := pend("0.2.0", 100, 10)
		a.shouldSwap(p, true, []string{"agent turns=1 (sess-a)"})
		if a.shouldSwap(p, true, []string{"agent turns=1 (sess-a)"}) {
			t.Fatal("swapped while an agent turn was running")
		}
		if !a.shouldSwap(p, true, nil) {
			t.Fatal("did not swap once the turn finished")
		}
	})

	t.Run("a failed handover is never retried for the same file", func(t *testing.T) {
		// Otherwise a binary that cannot boot is handed over to every 15
		// seconds for as long as it sits there.
		var a autoSwapper
		p := pend("0.2.0", 100, 10)
		a.shouldSwap(p, true, nil)
		if !a.shouldSwap(p, true, nil) {
			t.Fatal("setup: expected a swap")
		}
		a.markFailed(p)
		a.shouldSwap(p, true, nil)
		if a.shouldSwap(p, true, nil) {
			t.Fatal("retried a file that already failed to take over")
		}
		// A NEW build at the same path is a different file, and gets its turn.
		newer := pend("0.2.1", 120, 20)
		a.shouldSwap(newer, true, nil)
		if !a.shouldSwap(newer, true, nil) {
			t.Fatal("refused a new build after an earlier one failed")
		}
	})

	t.Run("nothing pending is not a swap", func(t *testing.T) {
		var a autoSwapper
		if a.shouldSwap(daemon.Pending{}, false, nil) {
			t.Fatal("swapped with nothing waiting")
		}
	})
}
