package upgrade

import (
	"os"
	"testing"
	"time"
)

func TestDrainStateHoldsSession(t *testing.T) {
	listed := DrainState{Sessions: []string{"sub-a"}, Outstanding: []string{"agent turns=1 (sub-a)"}}
	if !listed.HoldsSession("sub-a") {
		t.Fatal("a listed session must be held")
	}
	if listed.HoldsSession("sub-b") {
		t.Fatal("a structured list is exact: an unlisted session is not held")
	}
	if listed.HoldsSession("") {
		t.Fatal("an empty session id is never held")
	}

	// A record from a binary that predates Sessions only has the text. Any
	// agent turn left there may be the child's, so it counts as held.
	legacy := DrainState{Outstanding: []string{"agent turns=1 (sess-x)"}}
	if !legacy.HoldsSession("sub-b") {
		t.Fatal("legacy record with agent turns must err towards held")
	}
	if (DrainState{Outstanding: []string{"delegations=1"}}).HoldsSession("sub-b") {
		t.Fatal("no agent turns and no list: nothing to hold")
	}
}

func TestPredecessorHolds(t *testing.T) {
	dir := t.TempDir()
	since := time.Now()

	// The parent process stands in for a live predecessor.
	PublishDrainState(dir, os.Getppid(), since, nil, "sub-a")
	if !PredecessorHolds(dir, "sub-a") {
		t.Fatal("a fresh record from a live pid listing the session must hold it")
	}
	if PredecessorHolds(dir, "sub-b") {
		t.Fatal("an unlisted session is not held")
	}

	// This process never judges itself through its own record.
	PublishDrainState(dir, os.Getpid(), since, nil, "sub-a")
	if PredecessorHolds(dir, "sub-a") {
		t.Fatal("own drain record must not count as a predecessor")
	}

	PublishDrainState(dir, 999999, since, nil, "sub-a")
	if PredecessorHolds(dir, "sub-a") {
		t.Fatal("a dead pid holds nothing")
	}

	PublishDrainStateAt(dir, os.Getppid(), since, nil, time.Now().Add(-time.Minute), "sub-a")
	if PredecessorHolds(dir, "sub-a") {
		t.Fatal("a stale record holds nothing")
	}

	if PredecessorHolds("", "sub-a") {
		t.Fatal("no dir, no predecessor")
	}
}
