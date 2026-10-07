package delegation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// countByStatus reports how many of ids are in each status right now.
func countByStatus(t *testing.T, r *Repo, ids []string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, id := range ids {
		row, err := r.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		out[row.Status]++
	}
	return out
}

// The cap is per CONVERSATION. Every top-level delegate call from a
// leader starts its own tree, so a per-tree count let four background
// delegations fired together all run under sub_agents_max_parallel=1.
// Fired concurrently on purpose: the check and the claim must be one step.
func TestFourTopLevelDelegationsShareOneConversationSlot(t *testing.T) {
	s, r := serialService(t, &scriptedStream{hold: true})
	ctx := context.Background()

	var (
		mu  sync.Mutex
		ids []string
		wg  sync.WaitGroup
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := baseReq()
			req.Mode = ModeAsync
			req.DeliverySink = SinkNone
			res, err := s.Run(ctx, req)
			if err != nil {
				t.Errorf("run: %v", err)
				return
			}
			mu.Lock()
			ids = append(ids, res.DelegationID)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 4 {
		t.Fatalf("got %d delegations, want 4", len(ids))
	}

	got := countByStatus(t, r, ids)
	if got[entity.DelegationRunning] != 1 || got[entity.DelegationQueued] != 3 {
		t.Fatalf("statuses = %v, want 1 running + 3 queued", got)
	}

	// They drain one at a time: finishing the runner starts exactly one more.
	for done := 1; done <= 3; done++ {
		for _, id := range ids {
			row, _ := r.Get(ctx, id)
			if row.Status == entity.DelegationRunning {
				s.finish(ctx, row, entity.DelegationRunning, entity.DelegationDone, "ok", "", 1)
				break
			}
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if c := countByStatus(t, r, ids); c[entity.DelegationDone] == done && c[entity.DelegationRunning] == 1 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if c := countByStatus(t, r, ids); c[entity.DelegationRunning] != 1 || c[entity.DelegationDone] != done {
			t.Fatalf("after %d done: %v, want exactly one running", done, c)
		}
	}
}

// A different conversation has its own slot: one busy room must not
// stall another.
func TestOtherConversationHasItsOwnSlot(t *testing.T) {
	s, _ := serialService(t, &scriptedStream{hold: true})
	ctx := context.Background()

	req := baseReq()
	req.Mode, req.DeliverySink = ModeAsync, SinkNone
	if res, err := s.Run(ctx, req); err != nil || res.Status != entity.DelegationRunning {
		t.Fatalf("first = %+v, %v; want running", res, err)
	}
	other := baseReq()
	other.Mode, other.DeliverySink = ModeAsync, SinkNone
	other.ParentSessionID = "another-room"
	if res, err := s.Run(ctx, other); err != nil || res.Status != entity.DelegationRunning {
		t.Fatalf("other room = %+v, %v; want running", res, err)
	}
}

// A continue is a sub-agent starting work like any other: in a busy
// conversation it queues instead of reopening straight to running.
func TestContinueQueuesWhenTheConversationIsBusy(t *testing.T) {
	s, r := serialService(t, &scriptedStream{hold: true})
	ctx := context.Background()

	busy := seedDelegation(t, r, "busy", "busy", entity.DelegationRunning, 0)
	busy.ParentSessionID = "leader"
	finished := seedDelegation(t, r, "old", "old", entity.DelegationDone, 2)
	finished.ParentSessionID, finished.Mode = "leader", ModeAsync
	for _, d := range []*entity.AgentDelegation{busy, finished} {
		if err := r.SaveDelegationForTest(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.Continue(ctx, ContinueRequest{DelegationID: "old", Task: "keep going", ActorID: "user-1"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Status != entity.DelegationQueued || !res.Continued {
		t.Fatalf("result = %+v, want a queued continuation", res)
	}
	if row, _ := r.Get(ctx, "old"); row.Status != entity.DelegationQueued {
		t.Fatalf("row status = %q, want queued", row.Status)
	}

	// The busy one finishes; the queued continuation takes the slot.
	s.finish(ctx, busy, entity.DelegationRunning, entity.DelegationDone, "ok", "", 1)
	waitFor(t, 10*time.Second, func() bool {
		row, err := r.Get(ctx, "old")
		return err == nil && row.Status != entity.DelegationQueued
	})
}
