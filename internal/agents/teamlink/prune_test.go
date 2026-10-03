package teamlink

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// fakeClock is a movable clock for the janitor.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func TestJanitorDropsOldTasksContextsAndStoredTasks(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	h.now = clk.now
	ctx := context.Background()

	old, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "old"})
	if err != nil {
		t.Fatal(err)
	}
	// An @mention in Anton's reply would continue the old exchange…
	h.mu.Lock()
	_, hasLast := h.last["a-anton"]
	h.mu.Unlock()
	if !hasLast {
		t.Fatal("reply exchange not remembered")
	}

	// …but not after LastTTL, and the task and its context not after
	// TaskTTL. Two rotations take the stored task with them.
	clk.add(TaskTTL + time.Minute)
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "anton", Text: "new"}); err != nil {
		t.Fatal(err)
	}
	clk.add(TaskTTL + time.Minute)
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", To: "vera", Text: "newer"}); err != nil {
		t.Fatal(err)
	}

	h.mu.Lock()
	_, taskKept := h.tasks[a2a.TaskID(old.TaskID)]
	_, ctxKept := h.contexts[old.ContextID]
	_, lastKept := h.last["a-anton"]
	h.mu.Unlock()
	if taskKept || ctxKept || lastKept {
		t.Fatalf("kept task=%v context=%v last=%v", taskKept, ctxKept, lastKept)
	}
	if _, err := h.GetTask(ctx, "a-cap", old.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("pruned task still readable: %v", err)
	}
	if _, err := h.handler("a-anton").GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(old.TaskID)}); err == nil {
		t.Fatal("task store still holds the pruned task")
	}
}

func TestJanitorCapsTaskCount(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	base := time.Unix(1_000_000, 0)
	h.now = func() time.Time { return base }
	h.mu.Lock()
	for i := 0; i < MaxTasks+5; i++ {
		h.tasks[a2a.TaskID(fmt.Sprint("t", i))] = &taskRef{touched: base.Add(time.Duration(i) * time.Second)}
	}
	h.pruneLocked()
	n := len(h.tasks)
	_, oldest := h.tasks["t0"]
	_, newest := h.tasks[a2a.TaskID(fmt.Sprint("t", MaxTasks+4))]
	h.mu.Unlock()
	if n != MaxTasks || oldest || !newest {
		t.Fatalf("len=%d oldest kept=%v newest kept=%v", n, oldest, newest)
	}
}
