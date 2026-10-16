package teamlink

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchProgressCountsOpenBatches(t *testing.T) {
	h, turns, note := newFanHub(t, true)
	a := fanSend(t, h, "anton", nil)
	fanSend(t, h, "vera", nil)
	fanSend(t, h, "nina", nil)
	if r, n := h.BatchProgress("sess-cap"); r != 0 || n != 3 {
		t.Fatalf("progress = %d/%d, want 0/3", r, n)
	}
	turns.line("anton") <- "anton: done"
	settled(t, h, a.TaskID, "completed")
	if r, n := h.BatchProgress("sess-cap"); r != 1 || n != 3 {
		t.Fatalf("progress = %d/%d, want 1/3", r, n)
	}
	if r, n := h.BatchProgress("other"); r != 0 || n != 0 {
		t.Fatalf("other session = %d/%d", r, n)
	}
	quiet(t, note, 0)
}

func TestHiddenTeammatesNoteOnDelivery(t *testing.T) {
	for _, hide := range []bool{true, false} {
		h, turns, note := newFanHub(t, true)
		h.HideTeammates = func(string) bool { return hide }
		a := fanSend(t, h, "anton", nil)
		v := fanSend(t, h, "vera", nil)
		turns.line("anton") <- "anton: done"
		settled(t, h, a.TaskID, "completed")
		turns.line("vera") <- "vera: fine"
		settled(t, h, v.TaskID, "completed")
		got := quiet(t, note, 1)[0]
		if strings.Contains(got, hiddenTeammatesNote) != hide {
			t.Fatalf("hide=%v, delivery:\n%s", hide, got)
		}
	}
}

// S1: the hide hook may read the database, so it is never asked under the
// Hub lock: a hook that takes the lock itself must not block.
func TestHideTeammatesNotUnderHubLock(t *testing.T) {
	h, turns, note := newFanHub(t, true)
	var underLock atomic.Bool
	h.HideTeammates = func(string) bool {
		done := make(chan struct{})
		go func() { h.LiveTasks(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			underLock.Store(true)
		}
		return true
	}
	a := fanSend(t, h, "anton", nil)
	v := fanSend(t, h, "vera", nil)
	turns.line("anton") <- "anton: done"
	settled(t, h, a.TaskID, "completed")
	turns.line("vera") <- "vera: fine"
	settled(t, h, v.TaskID, "completed")
	got := quiet(t, note, 1)[0]
	if underLock.Load() {
		t.Fatal("HideTeammates was called while the Hub lock was held")
	}
	if !strings.Contains(got, hiddenTeammatesNote) {
		t.Fatalf("delivery lacks the note:\n%s", got)
	}
}
