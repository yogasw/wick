package api

import (
	"testing"

	"github.com/yogasw/wick/internal/pkg/daemon"
)

// The watcher must refuse what `reload --binary` refuses. Before this, the
// unattended path was the lax one: a binary the CLI would not install was
// exec'd automatically, every 15 seconds.
func TestShouldSwapRefusesBlockedBuild(t *testing.T) {
	p := daemon.Pending{Path: "/usr/bin/app", Version: "", Size: 10, ModTime: 1, Blocked: "no version", Want: "0.1.345"}
	var a autoSwapper
	// Seen once, stable, nothing running — every other condition satisfied.
	a.seen = p
	if a.shouldSwap(p, true, nil) {
		t.Fatal("a blocked build must never be handed over to automatically")
	}
	// Want is asserted alongside the refusal because it is what makes the
	// refusal actionable: "blocked" tells an operator to stop, Want tells
	// them what would go.
	got := a.status(p, true, nil, false)
	if got.State != "blocked" || got.Note == "" {
		t.Fatalf("status = %+v, want state=blocked with a note", got)
	}
	if got.Want != p.Want {
		t.Fatalf("status.Want = %q, want %q — a blocked build must still say what it would accept", got.Want, p.Want)
	}
}

// The warning about a blocked build is said once per file. It used to run on
// every tick, which on a binary that sits there unchanged is one fact
// repeated every 15 seconds until it drowns out the rest of the log.
func TestBlockedBuildIsWarnedAboutOncePerFile(t *testing.T) {
	p := daemon.Pending{Path: "/usr/bin/app", Version: "", Size: 10, ModTime: 1, Blocked: "no version"}
	var a autoSwapper

	if !a.shouldWarnBlocked(p) {
		t.Fatal("the first sighting of a blocked build must be reported")
	}
	for i := 0; i < 5; i++ {
		if a.shouldWarnBlocked(p) {
			t.Fatalf("tick %d repeated the warning for a file that has not changed", i+2)
		}
	}

	// A REBUILD at the same path is a different file, and is news again —
	// otherwise a second bad build would be applied in silence.
	rebuilt := p
	rebuilt.ModTime = 2
	if !a.shouldWarnBlocked(rebuilt) {
		t.Fatal("a new blocked build at the same path must be reported again")
	}
	if a.shouldWarnBlocked(rebuilt) {
		t.Fatal("the new file must then go quiet too")
	}
}

func TestShouldSwapAllowsNormalBuild(t *testing.T) {
	p := daemon.Pending{Path: "/usr/bin/app", Version: "0.1.344", Size: 10, ModTime: 1}
	var a autoSwapper
	if a.shouldSwap(p, true, nil) {
		t.Fatal("first sighting must wait one interval")
	}
	if !a.shouldSwap(p, true, nil) {
		t.Fatal("a stable, unblocked build with nothing running must swap")
	}
}
