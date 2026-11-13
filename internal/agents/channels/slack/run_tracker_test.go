package slack

import (
	"sync"
	"testing"
	"time"
)

// A run registered while a Reload is already waiting on an idle tracker
// (a connect racing a rollback) must not race: with a sync.WaitGroup this
// is an Add from zero concurrent with Wait, which the race detector flags.
func TestRunTrackerAddRacingWait(t *testing.T) {
	var r runTracker
	for i := 0; i < 200; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); waitRuns(&r, time.Second) }()
		go func() { defer wg.Done(); r.add(); r.done() }()
		wg.Wait()
	}
}

// waitRuns waits for a live run, returns at once when none is live, and
// gives up after its timeout on a run that does not end.
func TestRunTrackerWaits(t *testing.T) {
	var r runTracker
	if !waitRuns(&r, 10*time.Millisecond) {
		t.Fatal("no live run must not block")
	}
	r.add()
	if waitRuns(&r, 20*time.Millisecond) {
		t.Fatal("a live run must block until its timeout")
	}
	go func() { time.Sleep(10 * time.Millisecond); r.done() }()
	if !waitRuns(&r, time.Second) {
		t.Fatal("waitRuns missed the run ending")
	}
	r.add()
	r.add()
	r.done()
	if waitRuns(&r, 20*time.Millisecond) {
		t.Fatal("one of two runs is still live")
	}
	r.done()
	if !waitRuns(&r, time.Second) {
		t.Fatal("both runs ended")
	}
}
