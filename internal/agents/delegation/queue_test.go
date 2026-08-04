package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// A parent waiting on a synchronous child is not doing work and must not
// hold a slot — otherwise a serial room deadlocks the moment a sub-agent
// delegates: the parent owns the only slot while its child queues behind
// it.
func TestBlockedRowsDoNotCountAsActive(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	seed := func(id string, blocked bool) {
		d := &entity.AgentDelegation{
			ID: id, RootID: "root1", ParentSessionID: "leader",
			ProfileKey: "researcher", ChildSessionID: "sub-" + id,
			Status: entity.DelegationRunning, Blocked: blocked,
			StartedAt: time.Now().UTC(),
		}
		if err := r.Create(ctx, d); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("d1", false)
	seed("d2", true)

	n, err := r.CountActiveByRoot(ctx, "root1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("active = %d, want 1 (blocked row excluded)", n)
	}

	if err := r.SetBlocked(ctx, "d2", false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n, _ = r.CountActiveByRoot(ctx, "root1"); n != 2 {
		t.Fatalf("active after clear = %d, want 2", n)
	}
}

// A queued row is waiting, not working. Counting it as active would make
// a serial room admit nothing at all: the first queued item would itself
// fill the only slot.
func TestQueuedRowsDoNotCountAsActive(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedDelegation(t, r, "q1", "root1", entity.DelegationQueued, 0)

	n, err := r.CountActiveByRoot(ctx, "root1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("active = %d, want 0", n)
	}
}

// FIFO by enqueue time, with no priority column anywhere: the order a
// leader dispatched work in is the order it expects it back in.
func TestQueueIsFIFOByStartedAt(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	base := time.Now().UTC()
	for i, id := range []string{"q1", "q2", "q3"} {
		d := &entity.AgentDelegation{
			ID: id, RootID: "root1", ParentSessionID: "leader",
			ProfileKey: "researcher", ChildSessionID: "sub-" + id,
			Status: entity.DelegationQueued,
			// Descending insert order on purpose: the query must sort,
			// not rely on insertion order.
			StartedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := r.Create(ctx, d); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	head, err := r.OldestQueued(ctx, "root1")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head == nil || head.ID != "q1" {
		t.Fatalf("head = %v, want q1", head)
	}
	if pos, _ := r.QueuePosition(ctx, "root1", "q3"); pos != 3 {
		t.Fatalf("q3 position = %d, want 3", pos)
	}
	if pos, _ := r.QueuePosition(ctx, "root1", "q1"); pos != 1 {
		t.Fatalf("q1 position = %d, want 1", pos)
	}
	rows, err := r.ListQueued(ctx, "root1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 || rows[0].ID != "q1" || rows[2].ID != "q3" {
		t.Fatalf("ListQueued order wrong: %v", rows)
	}
}

// A row that is not queued has no position. 0 rather than an error: the
// panel asks for a position on every row, and "running" is not a fault.
func TestQueuePositionIsZeroForNonQueuedRows(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedDelegation(t, r, "d1", "root1", entity.DelegationRunning, 0)

	if pos, err := r.QueuePosition(ctx, "root1", "d1"); pos != 0 || err != nil {
		t.Fatalf("running position = %d, %v; want 0, nil", pos, err)
	}
	if pos, err := r.QueuePosition(ctx, "root1", "missing"); pos != 0 || err == nil {
		t.Fatalf("missing = %d, %v; want 0 and an error", pos, err)
	}
}

// An empty queue is a normal state, not a lookup failure.
func TestOldestQueuedIsNilWhenEmpty(t *testing.T) {
	r := testRepo(t)
	head, err := r.OldestQueued(context.Background(), "root1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if head != nil {
		t.Fatalf("head = %v, want nil", head)
	}
}

// serialService builds a service that runs ONE sub-agent at a time.
func serialService(t *testing.T, stream EventStream) (*Service, *Repo) {
	t.Helper()
	r := testRepo(t)
	seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.MaxParallel = 1
	return &Service{
		Repo: r, Runner: &fakeRunner{}, Stream: stream, Tokens: &fakeTokens{},
		Tags: staticTags{tags: []string{"a"}}, Limits: lim,
	}, r
}

// A fan-out beyond the parallel cap queues instead of being refused.
// Refusing would throw away three quarters of a four-way mention fan-out.
func TestParallelOverflowQueuesInsteadOfRefusing(t *testing.T) {
	// hold keeps the first sub-agent running so the room stays busy.
	held := &scriptedStream{hold: true}
	s, r := serialService(t, held)
	ctx := context.Background()

	req := baseReq()
	req.Mode = ModeAsync
	req.DeliverySink = SinkNone
	first, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Status != entity.DelegationRunning {
		t.Fatalf("first status = %q, want running", first.Status)
	}

	// Second call joins the same tree, so it competes for the same slot.
	req2 := baseReq()
	req2.Mode = ModeAsync
	req2.DeliverySink = SinkNone
	req2.RootID = first.DelegationID
	req2.Depth = 1
	second, err := s.Run(ctx, req2)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Status != entity.DelegationQueued {
		t.Fatalf("second status = %q, want queued", second.Status)
	}
	if second.QueuePosition != 1 {
		t.Fatalf("queue position = %d, want 1", second.QueuePosition)
	}
	if second.Note == "" {
		t.Fatal("a queued result with no note leaves the leader guessing")
	}

	row, err := r.Get(ctx, second.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != entity.DelegationQueued {
		t.Fatalf("row status = %q, want queued", row.Status)
	}
}

// The deadlock this whole design exists to avoid: a sub-agent delegating
// synchronously in a room with one slot. The parent is waiting, not
// working, so it must release its slot for the child.
func TestSyncChildOfSubAgentDoesNotDeadlock(t *testing.T) {
	done := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "child answer"},
		{Type: event.Done},
	}}
	s, r := serialService(t, done)
	ctx := context.Background()

	// A sub-agent already running and holding the only slot.
	parent := seedDelegation(t, r, "parent", "root-1", entity.DelegationRunning, 0)
	parent.ChildSessionID = "sub-parent"
	if err := r.SaveDelegationForTest(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	// That sub-agent now delegates synchronously.
	req := baseReq()
	req.ParentSessionID = "sub-parent"
	req.RootID = "root-1"
	req.Depth = 1

	deadline := time.AfterFunc(15*time.Second, func() {
		panic("sync child of a sub-agent deadlocked on the parallel slot")
	})
	defer deadline.Stop()

	res, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("nested run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want done", res.Status)
	}

	// The parent's slot is handed back once the child returns.
	got, err := r.Get(ctx, "parent")
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if got.Blocked {
		t.Fatal("parent left blocked after its child finished")
	}
}

// A parent that fires an ASYNC child keeps working, so it keeps its slot
// and the child queues normally.
func TestAsyncChildOfSubAgentDoesNotUnblockTheParent(t *testing.T) {
	held := &scriptedStream{hold: true}
	s, r := serialService(t, held)
	ctx := context.Background()

	parent := seedDelegation(t, r, "parent", "root-1", entity.DelegationRunning, 0)
	parent.ChildSessionID = "sub-parent"
	if err := r.SaveDelegationForTest(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	req := baseReq()
	req.ParentSessionID = "sub-parent"
	req.RootID = "root-1"
	req.Depth = 1
	req.Mode = ModeAsync
	req.DeliverySink = SinkNone

	res, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationQueued {
		t.Fatalf("status = %q, want queued (the parent still holds the slot)", res.Status)
	}
	got, _ := r.Get(ctx, "parent")
	if got.Blocked {
		t.Fatal("an async child must not block its parent")
	}
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// The queue drains on its own: finishing one sub-agent starts the next.
func TestQueuedAsyncStartsWhenASlotFrees(t *testing.T) {
	done := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "answer"},
		{Type: event.Done},
	}}
	s, r := serialService(t, done)
	ctx := context.Background()

	// A running sibling occupies the only slot.
	seedDelegation(t, r, "busy", "root-1", entity.DelegationRunning, 0)

	req := baseReq()
	req.Mode = ModeAsync
	req.DeliverySink = SinkNone
	req.RootID = "root-1"
	req.Depth = 1
	queued, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if queued.Status != entity.DelegationQueued {
		t.Fatalf("status = %q, want queued", queued.Status)
	}

	// The sibling finishes; the poke starts the queued row.
	busy, _ := r.Get(ctx, "busy")
	s.finish(ctx, busy, entity.DelegationRunning, entity.DelegationDone, "done", "", 1)

	waitFor(t, 10*time.Second, func() bool {
		row, err := r.Get(ctx, queued.DelegationID)
		return err == nil && row.Status != entity.DelegationQueued
	})
}

// An item that waited while its tree spent the budget must fail visibly
// with the refusal as its result, not run as though nothing changed.
func TestQueuedRowIsRecheckedAgainstTheGovernorAtStart(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	lim := s.Limits
	lim.RootBudget = 5
	s.Limits = lim

	// Queued row waiting behind a sibling that burns the whole budget.
	seedDelegation(t, r, "spender", "root-1", entity.DelegationDone, 5)
	q := seedDelegation(t, r, "q1", "root-1", entity.DelegationQueued, 0)
	q.Mode = ModeAsync
	if err := r.SaveDelegationForTest(ctx, q); err != nil {
		t.Fatalf("seed queued: %v", err)
	}

	s.startNextQueued(ctx, "root-1")

	row, err := r.Get(ctx, "q1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != entity.DelegationStoppedBudget {
		t.Fatalf("status = %q, want %q", row.Status, entity.DelegationStoppedBudget)
	}
	if row.Result == "" {
		t.Fatal("a budget-stopped queued row must carry the refusal as its result")
	}
}

// The sweeper releases a slot held by a delegation that already finished
// — the crash case the deferred clear cannot cover.
func TestSweeperClearsStaleBlockedFlags(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()

	stale := seedDelegation(t, r, "ghost", "root-1", entity.DelegationDone, 1)
	stale.Blocked = true
	if err := r.SaveDelegationForTest(ctx, stale); err != nil {
		t.Fatalf("seed: %v", err)
	}

	NewDelegationSweeper(s, time.Second).Pass(ctx)

	got, _ := r.Get(ctx, "ghost")
	if got.Blocked {
		t.Fatal("sweeper left a phantom slot holder behind")
	}
}

// Cancelling a queued item is total: no process is ever spawned, and a
// later sweep must not start it anyway.
func TestInterruptQueuedRowNeverSpawns(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	run := s.Runner.(*fakeRunner)
	ctx := context.Background()

	q := seedDelegation(t, r, "q1", "root-1", entity.DelegationQueued, 0)
	q.Mode = ModeAsync
	if err := r.SaveDelegationForTest(ctx, q); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := s.Interrupt(ctx, "q1", "user-1", false)
	if err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	if out != OutcomeDequeued {
		t.Fatalf("outcome = %q, want %q", out, OutcomeDequeued)
	}

	row, _ := r.Get(ctx, "q1")
	if row.Status != entity.DelegationInterrupted {
		t.Fatalf("status = %q, want interrupted", row.Status)
	}
	if k := run.killed(); len(k) != 0 {
		t.Fatalf("kills = %v, want none — a queued row has no process", k)
	}

	// A later sweep must not resurrect it.
	NewDelegationSweeper(s, time.Second).Pass(ctx)
	row, _ = r.Get(ctx, "q1")
	if row.Status != entity.DelegationInterrupted {
		t.Fatalf("sweeper restarted a cancelled row: %q", row.Status)
	}
}

// From a leader's side "not ready" is ONE state. Splitting queued from
// running would make every caller handle a third case for no gain.
func TestCollectOnQueuedRowIsPending(t *testing.T) {
	s, r := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, r, "q1", "root-1", entity.DelegationQueued, 0)

	res, err := s.Collect(ctx, "q1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !res.Pending {
		t.Fatal("a queued delegation must collect as pending")
	}
	if res.Result != "" {
		t.Fatalf("result = %q, want empty", res.Result)
	}
}

// SetBlocked is guarded to running rows: a terminal row must never be
// resurrected into the slot count by a late unblock.
func TestSetBlockedIgnoresTerminalRows(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedDelegation(t, r, "d1", "root1", entity.DelegationDone, 1)

	if err := r.SetBlocked(ctx, "d1", true); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, err := r.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Blocked {
		t.Fatal("terminal row was marked blocked")
	}
}
