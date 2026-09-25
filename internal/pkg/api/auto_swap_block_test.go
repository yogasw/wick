package api

import (
	"testing"

	"github.com/yogasw/wick/internal/pkg/daemon"
)

// The watcher must refuse what `reload --binary` refuses. Before this, the
// unattended path was the lax one: a binary the CLI would not install was
// exec'd automatically, every 15 seconds.
func TestShouldSwapRefusesBlockedBuild(t *testing.T) {
	p := daemon.Pending{Path: "/usr/bin/app", Version: "", Size: 10, ModTime: 1, Blocked: "no version"}
	var a autoSwapper
	// Seen once, stable, nothing running — every other condition satisfied.
	a.seen = p
	if a.shouldSwap(p, true, nil) {
		t.Fatal("a blocked build must never be handed over to automatically")
	}
	if got := a.status(p, true, nil, false); got.State != "blocked" || got.Note == "" {
		t.Fatalf("status = %+v, want state=blocked with a note", got)
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
