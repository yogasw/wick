package delegation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// bgRecorder captures the background hooks. Guarded because the finish-side
// report fires from the async run's own goroutine.
type bgRecorder struct {
	mu      sync.Mutex
	starts  []BackgroundStart
	changes [][]Survivor
}

func (b *bgRecorder) wire(s *Service) {
	s.OnBackgroundStart = func(parent string, st BackgroundStart) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.starts = append(b.starts, st)
	}
	s.OnBackgroundChange = func(parent string, active []Survivor) {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.changes = append(b.changes, active)
	}
}

func (b *bgRecorder) snapshot() ([]BackgroundStart, [][]Survivor) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]BackgroundStart(nil), b.starts...), append([][]Survivor(nil), b.changes...)
}

// A background run announces itself once, reports itself active, and reports
// an empty set once it ends — the signal a channel clears its banner on.
func TestBackgroundRunAnnouncesAndClears(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	rec := &bgRecorder{}
	rec.wire(s)

	req := baseReq()
	req.Mode = ModeAsync
	req.DeliverySink = SinkNone
	req.Task = strings.Repeat("x", 200)
	if _, err := s.Run(context.Background(), req); err != nil {
		t.Fatalf("run: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, changes := rec.snapshot()
		if len(changes) >= 2 && len(changes[len(changes)-1]) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background set never reported empty after the run ended: %+v", changes)
		}
		time.Sleep(10 * time.Millisecond)
	}

	starts, changes := rec.snapshot()
	if len(starts) != 1 {
		t.Fatalf("start hook fired %d times, want exactly once", len(starts))
	}
	if starts[0].Queued || starts[0].Agent.ProfileKey != "researcher" || starts[0].Agent.Handle == "" {
		t.Fatalf("start = %+v, want a running researcher with a handle", starts[0])
	}
	if got := []rune(starts[0].Task); len(got) != startTaskRunes+1 {
		t.Fatalf("task excerpt is %d runes, want %d plus the ellipsis", len(got), startTaskRunes)
	}
	if len(changes[0]) != 1 || changes[0][0].ProfileKey != "researcher" {
		t.Fatalf("first report = %+v, want the new sub-agent active", changes[0])
	}
}

// Foreground work is already visible through the leader's live turn, so it
// must not ping or touch the background set.
func TestForegroundRunIsNotAnnounced(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "answer"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	rec := &bgRecorder{}
	rec.wire(s)

	if _, err := s.Run(context.Background(), baseReq()); err != nil {
		t.Fatalf("run: %v", err)
	}
	starts, changes := rec.snapshot()
	if len(starts) != 0 || len(changes) != 0 {
		t.Fatalf("foreground run reported starts=%v changes=%v, want none", starts, changes)
	}
}

// A background delegation that has to wait still gets its one start notice,
// flagged as queued, and counts as active work.
func TestQueuedBackgroundRunIsAnnouncedAsQueued(t *testing.T) {
	s, _ := serialService(t, &scriptedStream{hold: true})
	rec := &bgRecorder{}
	rec.wire(s)
	ctx := context.Background()

	req := baseReq()
	req.Mode, req.DeliverySink = ModeAsync, SinkNone
	first, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	req2 := baseReq()
	req2.Mode, req2.DeliverySink = ModeAsync, SinkNone
	req2.RootID, req2.Depth = first.DelegationID, 1
	if _, err := s.Run(ctx, req2); err != nil {
		t.Fatalf("second: %v", err)
	}

	starts, changes := rec.snapshot()
	if len(starts) != 2 || starts[0].Queued || !starts[1].Queued {
		t.Fatalf("starts = %+v, want running then queued", starts)
	}
	if last := changes[len(changes)-1]; len(last) != 2 {
		t.Fatalf("active set = %+v, want both the running and the queued one", last)
	}
}

// ActiveBackground is the rail's own filter: detached rows still queued or
// running. Finished and foreground rows never hold a banner.
func TestActiveBackgroundFiltersLikeTheRail(t *testing.T) {
	r := testRepo(t)
	s := &Service{Repo: r}
	ctx := context.Background()
	mk := func(id, status string, detached bool) {
		d := seedDelegation(t, r, id, "root", status, 0)
		d.Detached = detached
		d.Handle = id
		if err := r.SaveDelegationForTest(ctx, d); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	mk("bg-running", entity.DelegationRunning, true)
	mk("bg-queued", entity.DelegationQueued, true)
	mk("bg-done", entity.DelegationDone, true)
	mk("fg-running", entity.DelegationRunning, false)

	got, err := s.ActiveBackground(ctx, "parent")
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	names := map[string]bool{}
	for _, sv := range got {
		names[sv.Handle] = true
	}
	if len(got) != 2 || !names["bg-running"] || !names["bg-queued"] {
		t.Fatalf("active = %+v, want exactly bg-running and bg-queued", got)
	}
}

// The stale re-check drops a running row whose process is gone, but keeps a
// queued one: it has no process yet and is still work owed.
func TestRecheckBackgroundDropsDeadChildren(t *testing.T) {
	r := testRepo(t)
	s := &Service{Repo: r, AgentAlive: func(child, agent string) bool { return child == "child-alive" }}
	ctx := context.Background()
	mk := func(id, status, child string) {
		d := seedDelegation(t, r, id, "root", status, 0)
		d.Detached, d.Handle, d.ChildSessionID = true, id, child
		if err := r.SaveDelegationForTest(ctx, d); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	mk("alive", entity.DelegationRunning, "child-alive")
	mk("dead", entity.DelegationRunning, "child-dead")
	mk("waiting", entity.DelegationQueued, "child-waiting")

	got, err := s.RecheckBackground(ctx, "parent")
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	names := map[string]bool{}
	for _, sv := range got {
		names[sv.Handle] = true
	}
	if len(got) != 2 || !names["alive"] || !names["waiting"] {
		t.Fatalf("recheck = %+v, want alive and waiting only", got)
	}
}

// The roster's one-query picture: background rows only, grouped by parent in
// start order, dead running rows dropped, queued rows kept.
func TestLiveBackgroundByParent(t *testing.T) {
	r := testRepo(t)
	s := &Service{Repo: r, AgentAlive: func(child, agent string) bool { return child != "child-dead" }}
	ctx := context.Background()
	base := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	mk := func(id, parent, status, child string, detached bool, at int) {
		d := seedDelegation(t, r, id, "root-"+parent, status, 0)
		d.ParentSessionID, d.Detached, d.Handle, d.ChildSessionID = parent, detached, id, child
		d.StartedAt = base.Add(time.Duration(at) * time.Minute)
		if err := r.SaveDelegationForTest(ctx, d); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	mk("b-second", "p1", entity.DelegationQueued, "c2", true, 2)
	mk("a-first", "p1", entity.DelegationRunning, "c1", true, 1)
	mk("fg", "p1", entity.DelegationRunning, "c3", false, 3)
	mk("dead", "p1", entity.DelegationRunning, "child-dead", true, 4)
	mk("done", "p1", entity.DelegationDone, "c5", true, 5)
	mk("other", "p2", entity.DelegationRunning, "c6", true, 6)

	got, err := s.LiveBackgroundByParent(ctx)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	if p1 := strings.Join(got["p1"], ","); p1 != "a-first,b-second" {
		t.Fatalf("p1 = %q, want a-first,b-second", p1)
	}
	if p2 := strings.Join(got["p2"], ","); p2 != "other" {
		t.Fatalf("p2 = %q, want other", p2)
	}
	if len(got) != 2 {
		t.Fatalf("parents = %v, want p1 and p2 only", got)
	}
}
