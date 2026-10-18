package agents

import (
	"encoding/json"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
)

// A session that is neither queued nor running has no wait_state in the
// stream snapshot, and the event body carries the slot numbers the page
// shows ("claude/x 4/4") even with the resource guard off.
func TestWaitStateEvent(t *testing.T) {
	prevPool, prevGuard := globalPool, resourceGuard
	t.Cleanup(func() { globalPool, resourceGuard = prevPool, prevGuard })
	globalPool = pool.New(pool.PoolConfig{Layout: agentconfig.NewLayout(t.TempDir()), MaxConcurrent: 7})
	resourceGuard = nil

	if _, ok := waitingSessions()["s1"]; ok {
		t.Fatal("an idle session counts as waiting")
	}
	for _, ev := range snapshotEvents("s1") {
		if ev.Type == "wait_state" {
			t.Fatalf("idle session snapshot carries %#v", ev)
		}
	}

	ev := waitStateEvent("s1", "")
	if ev.Type != "wait_state" || ev.SessionID != "s1" {
		t.Fatalf("event = %#v", ev)
	}
	var w sessionWaitResp
	if err := json.Unmarshal([]byte(ev.Data), &w); err != nil {
		t.Fatal(err)
	}
	if w.QueuePos != 0 || w.GlobalMax != 7 || w.Hold {
		t.Fatalf("wait state = %+v", w)
	}
}
