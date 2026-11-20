package sessionworkspace

import (
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
)

// An instance with actions still waiting for an approver outlives an idle
// session; once nothing waits, it is reaped and the removal hook hears it.
func TestSweepKeepsHeldInstance(t *testing.T) {
	layout, _ := newLayout(t)
	now := time.Now()
	writeMeta(t, layout, "idle-held", session.StatusIdle, now.Add(-IdleGrace-time.Minute))
	in, err := Add(layout, "idle-held", Instance{BaseKey: "httprest", Label: "A"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	holding := true
	var mu sync.Mutex
	removed := make(chan string, 1)
	SetHeld(func(sid, id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return holding && sid == "idle-held" && id == in.ID
	})
	SetOnRemoved(func(sid, id string) { removed <- sid + "/" + id })
	t.Cleanup(func() { SetHeld(nil); SetOnRemoved(nil) })

	if remaining, err := sweepOnce(layout, now); err != nil || remaining != 1 {
		t.Fatalf("held sweep: remaining=%d err=%v", remaining, err)
	}
	if got, _ := List(layout, "idle-held"); len(got) != 1 {
		t.Fatalf("a held instance must survive the idle sweep, got %+v", got)
	}

	mu.Lock()
	holding = false
	mu.Unlock()
	if remaining, err := sweepOnce(layout, now); err != nil || remaining != 0 {
		t.Fatalf("released sweep: remaining=%d err=%v", remaining, err)
	}
	select {
	case got := <-removed:
		if got != "idle-held/"+in.ID {
			t.Fatalf("removal hook heard %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a reaped instance must reach the removal hook")
	}
}
