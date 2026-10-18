package teamlink

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// B6: a task the draining predecessor took AFTER this process started
// (its file appears later) is not left "working" for good: once the
// predecessor is gone it is settled failed as interrupted.
func TestTaskWrittenByPredecessorAfterStartIsSettled(t *testing.T) {
	dir := t.TempDir()
	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	h2.orphanPoll = 5 * time.Millisecond
	var draining atomic.Bool
	draining.Store(true)
	h2.PredecessorBusy = func(string) bool { return draining.Load() }
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	id, _ := sendWorking(t, dir) // the predecessor takes a task now
	time.Sleep(30 * time.Millisecond)
	ctx := context.Background()
	if got, err := h2.GetTask(ctx, "a-cap", id); err != nil || got.State != "working" {
		t.Fatalf("while the predecessor drains: %+v %v", got, err)
	}
	draining.Store(false)
	waitFor(t, "the task to settle", func() bool {
		got, err := h2.GetTask(ctx, "a-cap", id)
		return err == nil && got.State == "failed" && got.Reason == interruptedReason
	})
}

// A process that handed over settles no orphan: the successor may be
// running it.
func TestDrainingProcessSettlesNoOrphan(t *testing.T) {
	dir := t.TempDir()
	id, _ := sendWorking(t, dir)
	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	h2.orphanPoll = 5 * time.Millisecond
	h2.SelfDraining = func() bool { return true }
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if got, err := h2.GetTask(context.Background(), "a-cap", id); err != nil || got.State != "working" {
		t.Fatalf("draining process settled it: %+v %v", got, err)
	}
}
