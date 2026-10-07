package memreport

import (
	"reflect"
	"testing"
)

func p(pid, ppid int) Proc { return Proc{PID: pid, PPID: ppid, Name: "x"} }

func TestOwners_LabelsTheWholeSubtree(t *testing.T) {
	// 10 is an agent; 11 is its MCP server; 12 is a browser the MCP opened.
	procs := []Proc{p(1, 0), p(10, 1), p(11, 10), p(12, 11), p(20, 1)}
	got := Owners(procs, map[int]string{10: "yoga"})
	want := map[int]string{10: "yoga", 11: "yoga", 12: "yoga"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A process nobody's tree contains is ABSENT, not empty-labelled: the caller
// has to be able to tell "not a wick spawn" from "a wick spawn we could not
// name", and those get different cells in the UI.
func TestOwners_UnrelatedProcessIsAbsent(t *testing.T) {
	procs := []Proc{p(1, 0), p(10, 1), p(99, 1)}
	got := Owners(procs, map[int]string{10: "yoga"})
	if _, ok := got[99]; ok {
		t.Fatalf("unrelated pid labelled: %v", got)
	}
}

// A sub-agent spawned for someone else sits UNDER its parent, and its memory
// is that person's, not the parent owner's.
func TestOwners_NearestRootWins(t *testing.T) {
	procs := []Proc{p(10, 1), p(11, 10), p(20, 11), p(21, 20)}
	got := Owners(procs, map[int]string{10: "yoga", 20: "anggun"})
	for pid, want := range map[int]string{10: "yoga", 11: "yoga", 20: "anggun", 21: "anggun"} {
		if got[pid] != want {
			t.Errorf("pid %d = %q, want %q (all: %v)", pid, got[pid], want, got)
		}
	}
}

// The pool is read before the /proc scan, so a spawn can exit in between.
func TestOwners_RootAlreadyGoneIsSkipped(t *testing.T) {
	got := Owners([]Proc{p(1, 0)}, map[int]string{10: "yoga"})
	if len(got) != 0 {
		t.Fatalf("dead root produced labels: %v", got)
	}
}

// /proc is sampled without a lock; a reused PID can make a parent link loop.
func TestOwners_SurvivesAParentCycle(t *testing.T) {
	procs := []Proc{p(10, 11), p(11, 10)}
	done := make(chan map[int]string, 1)
	go func() { done <- Owners(procs, map[int]string{10: "yoga"}) }()
	got := <-done
	if got[10] != "yoga" || got[11] != "yoga" {
		t.Fatalf("got %v", got)
	}
}

func TestOwners_NoRootsIsEmpty(t *testing.T) {
	if got := Owners([]Proc{p(1, 0)}, nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
