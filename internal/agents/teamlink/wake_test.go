package teamlink

import (
	"context"
	"testing"
	"time"
)

func TestWakeMarks(t *testing.T) {
	h := &Hub{}
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if _, ok := h.TakeWakes("s", t0); ok {
		t.Fatal("no mark: nothing to take")
	}
	h.markWake("s", t0.Add(10*time.Second)) // queued while a turn that started at t0 runs
	if _, ok := h.TakeWakes("s", t0); ok {
		t.Fatal("the running turn started before the mark: it must not take it")
	}
	if !h.HasWake("s") {
		t.Fatal("the mark waits for the next turn")
	}
	got, ok := h.TakeWakes("s", t0.Add(11*time.Second))
	if !ok || !got.Equal(t0.Add(10*time.Second)) || h.HasWake("s") {
		t.Fatalf("the woken turn takes it once: %v %v %v", got, ok, h.HasWake("s"))
	}
	h.markWake("s", t0)
	h.unmarkWake("s", t0)
	if h.HasWake("s") {
		t.Fatal("an undelivered prompt leaves no mark")
	}
}

// "Summarize now" shows only while a holding batch has a reply in and a
// member at work, and FlushGroup hands over that batch alone.
func TestSummarizableAndFlushGroup(t *testing.T) {
	h, turns, note := newFanHub(t, true)
	a := fanSend(t, h, "anton", nil)
	fanSend(t, h, "vera", nil)
	sum := func() map[string]bool {
		out := map[string]bool{}
		for _, v := range h.SentFrom("sess-cap") {
			if v.Group != a.GroupID {
				t.Fatalf("task %s group = %q, want %q", v.TaskID, v.Group, a.GroupID)
			}
			out[v.TaskID] = v.Summarizable
		}
		return out
	}
	if s := sum(); s[a.TaskID] {
		t.Fatal("nothing in yet: not summarizable")
	}
	turns.line("anton") <- "anton: done"
	settled(t, h, a.TaskID, "completed")
	if s := sum(); !s[a.TaskID] {
		t.Fatal("one in, one working: summarizable")
	}
	if n := h.FlushGroup(context.Background(), "sess-other", a.GroupID); n != 0 {
		t.Fatalf("a foreign session flushed %d", n)
	}
	if n := h.FlushGroup(context.Background(), "sess-cap", a.GroupID); n != 1 {
		t.Fatalf("flush group = %d", n)
	}
	quiet(t, note, 1)
	if s := sum(); s[a.TaskID] {
		t.Fatal("after the flush nothing is held: not summarizable")
	}
	// fan_in each holds nothing.
	each := func(in *SendInput) { in.FanIn = FanInEach }
	e := fanSend(t, h, "nina", each)
	for _, v := range h.SentFrom("sess-cap") {
		if v.TaskID == e.TaskID && v.Summarizable {
			t.Fatal("fan_in each is never summarizable")
		}
	}
	// A delivered prompt left a wake mark for the turn it wakes.
	if !h.HasWake("sess-cap") {
		t.Fatal("the delivery must leave a wake mark")
	}
}
