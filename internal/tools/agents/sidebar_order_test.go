package agents

import (
	"reflect"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// Running rows lead, in last-use order among themselves; the rest follow
// by last use. The pool's in-memory LastActive beats a staler persisted
// one, since that is what moves mid-turn.
func TestOrderSidebarIDsRunningFirstThenLastActive(t *testing.T) {
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }
	sessions := map[string]session.Session{
		"old-idle":     {Meta: session.Meta{LastActive: at(1)}},
		"new-idle":     {Meta: session.Meta{LastActive: at(50)}},
		"old-working":  {Meta: session.Meta{LastActive: at(2)}},
		"new-spawning": {Meta: session.Meta{LastActive: at(40)}},
		"queued":       {Meta: session.Meta{LastActive: at(3), Status: "queued"}},
		"sub-working":  {Meta: session.Meta{LastActive: at(4)}},
		"pool-fresh":   {Meta: session.Meta{LastActive: at(5)}},
		"warm-idle":    {Meta: session.Meta{LastActive: at(30)}},
	}
	lc := map[string]view.SessionLifecycleVM{
		"old-working":  {Lifecycle: "working"},
		"new-spawning": {Lifecycle: "spawning"},
		"sub-working":  {Lifecycle: "idle", SubAgent: "working"},
		// Persisted meta says minute 5, the pool saw activity at minute 60.
		"pool-fresh": {Lifecycle: "killed", LastActiveMs: at(60).UnixMilli()},
		// A warm idle process is not running — it sorts by age.
		"warm-idle": {Lifecycle: "idle"},
	}
	// Registry order (meta LastActive desc) as the handler receives it.
	ids := []string{"new-idle", "new-spawning", "warm-idle", "pool-fresh", "sub-working", "queued", "old-working", "old-idle"}

	got := orderSidebarIDs(ids, sessions, lc)
	want := []string{
		"new-spawning", "sub-working", "queued", "old-working",
		"pool-fresh", "new-idle", "warm-idle", "old-idle",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderSidebarIDs =\n  %v\nwant\n  %v", got, want)
	}
}
