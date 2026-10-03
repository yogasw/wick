package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func mustCreate(t *testing.T, s *Store, m *entity.ScheduledMessage) *entity.ScheduledMessage {
	t.Helper()
	if m.OwnerUserID == "" {
		m.OwnerUserID = "u1"
	}
	if m.Message == "" {
		m.Message = "ping"
	}
	if m.RunAt.IsZero() {
		m.RunAt = time.Now().Add(time.Hour)
	}
	out, err := s.Create(context.Background(), m)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return out
}

func recurringEvery(sessionID string, every time.Duration) *entity.ScheduledMessage {
	return &entity.ScheduledMessage{
		SessionID: sessionID, Kind: entity.ScheduledKindRecurring,
		Status: entity.ScheduledStatusActive, IntervalMs: every.Milliseconds(),
	}
}

// The drawer lists what fires into the agent: its sessions and its
// project — and nothing aimed at another agent's chat.
func TestListTargetingScopesToTheAgent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mine := mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-a"})
	proj := mustCreate(t, s, &entity.ScheduledMessage{ProjectID: "proj-a", SessionMode: entity.ScheduledSessionNew})
	mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-b"})

	rows, err := s.ListTargeting(ctx, "proj-a", []string{"main-a", "side-a"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	if len(rows) != 2 || !got[mine.ID] || !got[proj.ID] {
		t.Fatalf("rows = %+v, want the session row and the project row only", rows)
	}
	if rows, _ := s.ListTargeting(ctx, "", nil, time.Time{}); rows != nil {
		t.Fatalf("empty scope must list nothing, got %d", len(rows))
	}
}

// Disabling an agent holds its running schedules; enabling releases only
// those — a schedule the user paused by hand stays paused.
func TestHoldAndReleaseKeepManualPauses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	running := mustCreate(t, s, recurringEvery("main-a", time.Hour))
	manual := mustCreate(t, s, recurringEvery("main-a", time.Hour))
	once := mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-a"})
	if err := s.SetPaused(ctx, manual.ID, true, time.Time{}); err != nil {
		t.Fatal(err)
	}

	n, err := s.HoldTargeting(ctx, "", []string{"main-a"})
	if err != nil || n != 2 {
		t.Fatalf("hold = %d, %v; want 2 (running + one-shot)", n, err)
	}
	for _, id := range []string{running.ID, once.ID} {
		m, _ := s.Get(ctx, id)
		if !m.Paused || !m.HeldByAgent {
			t.Fatalf("%s not held: %+v", id, m)
		}
	}
	// A held one-shot must not fire while the agent is off.
	if due, _ := s.ClaimDue(ctx, time.Now().Add(2*time.Hour), 10); len(due) != 0 {
		t.Fatalf("held rows were claimed: %d", len(due))
	}

	now := time.Now()
	n, err = s.ReleaseTargeting(ctx, "", []string{"main-a"}, now)
	if err != nil || n != 2 {
		t.Fatalf("release = %d, %v; want 2", n, err)
	}
	r, _ := s.Get(ctx, running.ID)
	if r.Paused || r.HeldByAgent || !r.RunAt.After(now) {
		t.Fatalf("running row not resumed with a future fire: %+v", r)
	}
	m, _ := s.Get(ctx, manual.ID)
	if !m.Paused || m.HeldByAgent {
		t.Fatalf("manually paused row woke up: %+v", m)
	}
}

// A hand resume of a held row makes it the user's: a later release must
// not touch it, and a later hold treats it like any running row.
func TestSetPausedClearsTheHold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := mustCreate(t, s, recurringEvery("main-a", time.Hour))
	if _, err := s.HoldTargeting(ctx, "", []string{"main-a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPaused(ctx, m.ID, true, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ReleaseTargeting(ctx, "", []string{"main-a"}, time.Now()); n != 0 {
		t.Fatalf("release woke a hand-paused row")
	}
}

func TestDeleteTargetingRemovesEveryStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	live := mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-a"})
	done := mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-a", Status: entity.ScheduledStatusDone})
	other := mustCreate(t, s, &entity.ScheduledMessage{SessionID: "main-b"})
	n, err := s.DeleteTargeting(ctx, "", []string{"main-a"})
	if err != nil || n != 2 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	for _, id := range []string{live.ID, done.ID} {
		if _, err := s.Get(ctx, id); err != ErrNotFound {
			t.Fatalf("%s survived: %v", id, err)
		}
	}
	if _, err := s.Get(ctx, other.ID); err != nil {
		t.Fatalf("another agent's row went too: %v", err)
	}
	if err := s.Delete(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, other.ID); err != ErrNotFound {
		t.Fatalf("second delete = %v", err)
	}
}

// A successful fire tells the hook where it landed; a failed one does not.
func TestRunnerNotifiesFiredHook(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()
	var fired []string
	SetFiredHook(func(_ context.Context, m entity.ScheduledMessage, sessionID string) {
		fired = append(fired, m.ID+"@"+sessionID)
	})
	t.Cleanup(func() { SetFiredHook(nil) })

	m := mustCreate(t, s, &entity.ScheduledMessage{SessionID: sid, RunAt: time.Now().Add(-time.Minute)})
	r.tick(ctx, zerologLogger{})
	if len(fired) != 1 || fired[0] != m.ID+"@"+sid {
		t.Fatalf("fired = %v", fired)
	}
	sender.failErr = context.Canceled
	mustCreate(t, s, &entity.ScheduledMessage{SessionID: sid, RunAt: time.Now().Add(-time.Minute)})
	r.tick(ctx, zerologLogger{})
	if len(fired) != 1 {
		t.Fatalf("a failed send reached the hook: %v", fired)
	}
}

// An edit that moves a schedule from cron to an interval (or back) keeps
// the new cadence: the parsed patch carries both fields, one empty.
func TestRescheduleSwitchesCadence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, err := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s1", Message: "x", RunAt: time.Now().Add(time.Hour),
		Kind: entity.ScheduledKindRecurring, Status: entity.ScheduledStatusActive, Cron: "0 9 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	iv, empty := time.Hour.Milliseconds(), ""
	if err := s.Reschedule(ctx, m.ID, SchedulePatch{IntervalMs: &iv, Cron: &empty}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, m.ID); got.IntervalMs != iv || got.Cron != "" {
		t.Fatalf("cron → every = %d %q", got.IntervalMs, got.Cron)
	}
	cr, zero := "30 8 * * 1", int64(0)
	if err := s.Reschedule(ctx, m.ID, SchedulePatch{IntervalMs: &zero, Cron: &cr}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, m.ID); got.IntervalMs != 0 || got.Cron != cr {
		t.Fatalf("every → cron = %d %q", got.IntervalMs, got.Cron)
	}
}
