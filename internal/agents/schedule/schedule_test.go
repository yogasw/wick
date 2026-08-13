package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.ScheduledMessage{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(db)
}

func TestStore_CreateGetCancel(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, err := s.Create(ctx, &entity.ScheduledMessage{
		SessionID:   "sess-1",
		OwnerUserID: "u1",
		Message:     "check in",
		RunAt:       time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == "" || m.Status != entity.ScheduledStatusPending || m.AgentName != "main" {
		t.Fatalf("defaults not applied: %+v", m)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil || got.ID != m.ID {
		t.Fatalf("get: %v %+v", err, got)
	}

	if err := s.Cancel(ctx, m.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Second cancel → ErrNotFound (no longer pending).
	if err := s.Cancel(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-cancel: want ErrNotFound, got %v", err)
	}
	got, _ = s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusCancelled {
		t.Fatalf("status after cancel: %q", got.Status)
	}
}

func TestStore_ClaimDue_OnlyPastAndOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	past, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "a", RunAt: now.Add(-time.Minute)})
	_, _ = s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "b", RunAt: now.Add(time.Hour)}) // future

	claimed, err := s.ClaimDue(ctx, now, 50)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != past.ID {
		t.Fatalf("want only the past row, got %d rows", len(claimed))
	}
	// Claim parks run_at in the future + stamps last_run_at/run_count; it does
	// NOT set a terminal status (the runner does that after delivery).
	if claimed[0].RunCount != 1 || claimed[0].LastRunAt == nil {
		t.Fatalf("claimed row not stamped: %+v", claimed[0])
	}

	// A second immediate claim returns nothing — the row is parked out of the
	// due window until the runner finalizes it.
	again, _ := s.ClaimDue(ctx, now, 50)
	if len(again) != 0 {
		t.Fatalf("double-claim: got %d, want 0", len(again))
	}

	// One-shot finalize → done.
	if err := s.Finalize(ctx, past.ID, entity.ScheduledKindOnce, time.Time{}, "s"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	got, _ := s.Get(ctx, past.ID)
	if got.Status != entity.ScheduledStatusDone {
		t.Fatalf("one-shot finalize status = %q, want done", got.Status)
	}
}

func TestStore_NegativeMaxRunsClampedToZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// A negative max_runs must not turn a capped recurring schedule into an
	// unbounded one (advance treats MaxRuns<=0 as "no cap"). BeforeCreate
	// clamps it to 0.
	m, err := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
		IntervalMs: (time.Hour).Milliseconds(), RunAt: time.Now(), MaxRuns: -5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.MaxRuns != 0 {
		t.Fatalf("negative max_runs not clamped: got %d, want 0", m.MaxRuns)
	}
}

func TestStore_RecurringClaimFinalizeReschedules(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "poll", Kind: entity.ScheduledKindRecurring,
		IntervalMs: (5 * time.Minute).Milliseconds(), RunAt: now.Add(-time.Second),
	})
	if m.Status != entity.ScheduledStatusActive {
		t.Fatalf("recurring create status = %q, want active", m.Status)
	}

	claimed, _ := s.ClaimDue(ctx, now, 50)
	if len(claimed) != 1 {
		t.Fatalf("claim recurring: got %d", len(claimed))
	}
	// Runner computes next fire and finalizes back to active.
	next := now.Add(5 * time.Minute)
	if err := s.Finalize(ctx, m.ID, entity.ScheduledKindRecurring, next, "s"); err != nil {
		t.Fatalf("finalize recurring: %v", err)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusActive {
		t.Fatalf("recurring status after finalize = %q, want active", got.Status)
	}
	if got.RunAt.Sub(next).Abs() > time.Second {
		t.Fatalf("run_at not advanced to next: %v vs %v", got.RunAt, next)
	}
	if got.RunCount != 1 {
		t.Fatalf("run_count = %d, want 1", got.RunCount)
	}
}

func TestStore_PauseResumeAndCancel(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
		IntervalMs: (time.Hour).Milliseconds(), RunAt: now.Add(time.Hour),
	})
	// Pause → not claimed even when due.
	if err := s.SetPaused(ctx, m.ID, true, time.Time{}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	claimed, _ := s.ClaimDue(ctx, now.Add(2*time.Hour), 50)
	if len(claimed) != 0 {
		t.Fatalf("paused schedule was claimed")
	}
	// Resume with a fresh run_at → claimable again.
	if err := s.SetPaused(ctx, m.ID, false, now.Add(-time.Second)); err != nil {
		t.Fatalf("resume: %v", err)
	}
	claimed, _ = s.ClaimDue(ctx, now, 50)
	if len(claimed) != 1 {
		t.Fatalf("resumed schedule not claimed: %d", len(claimed))
	}
	// Cancel a live recurring row.
	if err := s.Cancel(ctx, m.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusCancelled {
		t.Fatalf("cancel status = %q", got.Status)
	}
}

func TestStore_ListForOwner_Scope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, &entity.ScheduledMessage{SessionID: "s1", OwnerUserID: "u1", Message: "x", RunAt: time.Now().Add(time.Hour)})
	_, _ = s.Create(ctx, &entity.ScheduledMessage{SessionID: "s2", OwnerUserID: "u2", Message: "y", RunAt: time.Now().Add(time.Hour)})

	mine, _ := s.ListForOwner(ctx, "u1", "", false)
	if len(mine) != 1 || mine[0].OwnerUserID != "u1" {
		t.Fatalf("owner scope leaked: %+v", mine)
	}
	all, _ := s.ListForOwner(ctx, "", "", true)
	if len(all) != 2 {
		t.Fatalf("admin all-owners: got %d want 2", len(all))
	}
	scoped, _ := s.ListForOwner(ctx, "", "s2", true)
	if len(scoped) != 1 || scoped[0].SessionID != "s2" {
		t.Fatalf("session scope: %+v", scoped)
	}
}

// run_now exists so a schedule can be TESTED without waiting for the clock.
// It must make the row due immediately without redefining the schedule.
func TestStore_RunNow(t *testing.T) {
	ctx := context.Background()

	t.Run("makes a future one-shot due immediately", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", Message: "x", RunAt: time.Now().Add(24 * time.Hour),
		})
		// Not due yet.
		if due, _ := s.ClaimDue(ctx, time.Now(), 10); len(due) != 0 {
			t.Fatalf("claimed a future row: %d", len(due))
		}
		if err := s.RunNow(ctx, m.ID); err != nil {
			t.Fatalf("run now: %v", err)
		}
		due, _ := s.ClaimDue(ctx, time.Now(), 10)
		if len(due) != 1 || due[0].ID != m.ID {
			t.Fatalf("run_now did not make the row due: %d", len(due))
		}
	})

	t.Run("keeps the cadence: a manual fire does not redefine it", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
			Cron: "0 9 * * 1", RunAt: time.Now().Add(48 * time.Hour),
		})
		if err := s.RunNow(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.Cron != "0 9 * * 1" || got.MaxRuns != m.MaxRuns || got.Message != "x" {
			t.Fatalf("run_now changed the schedule definition: %+v", got)
		}
	})

	t.Run("un-pauses, because running it now plainly means run it", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
			IntervalMs: (time.Hour).Milliseconds(), RunAt: time.Now().Add(time.Hour),
		})
		_ = s.SetPaused(ctx, m.ID, true, time.Time{})
		if err := s.RunNow(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		due, _ := s.ClaimDue(ctx, time.Now(), 10)
		if len(due) != 1 {
			t.Fatalf("paused row not claimable after run_now: %d", len(due))
		}
	})

	t.Run("a terminal schedule cannot be run", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "x", RunAt: time.Now()})
		_ = s.Cancel(ctx, m.ID)
		if err := s.RunNow(ctx, m.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("run_now on a cancelled row: want ErrNotFound, got %v", err)
		}
	})
}

// A manual run goes through the ordinary claim+deliver path, so it lands in
// exactly the session the schedule's mode says it should.
func TestRunner_RunNowDelivers(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: sid, Message: "ping", RunAt: time.Now().Add(24 * time.Hour),
	})
	r.tick(ctx, zerologLogger{}) // nothing due yet
	if len(sender.calls) != 0 {
		t.Fatalf("fired before its time: %v", sender.calls)
	}

	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx, zerologLogger{})
	if len(sender.calls) != 1 || sender.calls[0] != sid+"|schedule|user|ping" {
		t.Fatalf("run_now delivery = %v", sender.calls)
	}
}

// Wake must be safe to call with no runner and to call repeatedly — it is a
// best-effort nudge, never a blocking handoff.
func TestRunner_WakeIsNonBlocking(t *testing.T) {
	WakeRunner() // no runner running: must not panic or block

	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	r := NewRunner(s, &fakeSender{layout: layout}, layout)
	for i := 0; i < 5; i++ {
		r.Wake() // buffered(1): extra wakes coalesce instead of blocking
	}
}

// The claim parks run_at ~100 years out so a crash between claim and finalize
// can't re-fire the row. That sentinel is internal: every terminal transition
// must unpark it, and NextRunAt() must never publish it.
func TestStore_ClaimSentinelNeverLeaks(t *testing.T) {
	ctx := context.Background()
	fireAt := time.Now().Add(-time.Second)

	t.Run("one-shot done keeps the time it fired", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "x", RunAt: fireAt})
		claimed, _ := s.ClaimDue(ctx, time.Now(), 10)
		if len(claimed) != 1 {
			t.Fatalf("claim: %d", len(claimed))
		}
		// While claimed, the row is parked and must report no next fire.
		mid, _ := s.Get(ctx, m.ID)
		if mid.NextRunAt() != nil {
			t.Fatalf("parked row published a next fire: %v", mid.NextRunAt())
		}
		if err := s.Finalize(ctx, m.ID, entity.ScheduledKindOnce, time.Time{}, "s"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.RunAt.Year() > time.Now().Year()+1 {
			t.Fatalf("done row kept the park sentinel: run_at = %v", got.RunAt)
		}
		if got.NextRunAt() != nil {
			t.Fatalf("done row published a next fire: %v", got.NextRunAt())
		}
	})

	t.Run("failed row unparks too", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "x", RunAt: fireAt})
		_, _ = s.ClaimDue(ctx, time.Now(), 10)
		if err := s.MarkFailed(ctx, m.ID, "boom"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.RunAt.Year() > time.Now().Year()+1 {
			t.Fatalf("failed row kept the park sentinel: run_at = %v", got.RunAt)
		}
		if got.NextRunAt() != nil {
			t.Fatalf("failed row published a next fire")
		}
	})

	t.Run("cancel mid-claim unparks", func(t *testing.T) {
		s := newTestStore(t)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
			IntervalMs: (time.Hour).Milliseconds(), RunAt: fireAt,
		})
		_, _ = s.ClaimDue(ctx, time.Now(), 10)
		if err := s.Cancel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.RunAt.Year() > time.Now().Year()+1 {
			t.Fatalf("cancelled row kept the park sentinel: run_at = %v", got.RunAt)
		}
	})

	t.Run("cancel before any fire keeps the scheduled time", func(t *testing.T) {
		s := newTestStore(t)
		want := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "x", RunAt: want})
		if err := s.Cancel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		// last_run_at is NULL here, so COALESCE must fall back to the
		// untouched run_at rather than zeroing it.
		if got.RunAt.UTC().Truncate(time.Second) != want {
			t.Fatalf("cancel clobbered run_at: got %v want %v", got.RunAt, want)
		}
	})

	t.Run("live recurring still publishes its next fire", func(t *testing.T) {
		s := newTestStore(t)
		next := time.Now().Add(5 * time.Minute)
		m, _ := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
			IntervalMs: (5 * time.Minute).Milliseconds(), RunAt: fireAt,
		})
		_, _ = s.ClaimDue(ctx, time.Now(), 10)
		if err := s.Finalize(ctx, m.ID, entity.ScheduledKindRecurring, next, "s"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.NextRunAt() == nil {
			t.Fatal("live recurring row must publish its next fire")
		}
	})
}

// A project job belongs to the PROJECT, not to the session that created it,
// so every session in that project must see it — otherwise switching to a
// sibling conversation makes the job vanish.
func TestStore_ListFiltered_ProjectJobIsCrossSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	// A project job created from s1, and a plain nudge aimed at s1.
	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		ProjectID: "p1", SessionMode: entity.ScheduledSessionNew,
		SourceSessionID: "s1", Message: "weekly report", RunAt: future,
	})
	_, _ = s.Create(ctx, &entity.ScheduledMessage{SessionID: "s1", Message: "nudge s1", RunAt: future})
	// A sibling project's job, and a nudge aimed at another session.
	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		ProjectID: "p2", SessionMode: entity.ScheduledSessionNew, Message: "other project", RunAt: future,
	})
	_, _ = s.Create(ctx, &entity.ScheduledMessage{SessionID: "s2", Message: "nudge s2", RunAt: future})

	msgs := func(rows []entity.ScheduledMessage) map[string]bool {
		out := map[string]bool{}
		for _, r := range rows {
			out[r.Message] = true
		}
		return out
	}

	// s2 is a DIFFERENT session in the same project — it never created the
	// job, but it must still see it.
	sibling, _ := s.ListFiltered(ctx, "", SessionScope{ID: "s2", ProjectID: "p1"}, "", true)
	got := msgs(sibling)
	if !got["weekly report"] {
		t.Fatalf("project job invisible from a sibling session in the same project: %v", got)
	}
	if !got["nudge s2"] {
		t.Fatalf("sibling session lost its own nudge: %v", got)
	}
	// Scoping must not leak: another session's nudge, and another project's
	// job, stay out.
	if got["nudge s1"] {
		t.Fatalf("another session's nudge leaked: %v", got)
	}
	if got["other project"] {
		t.Fatalf("another project's job leaked: %v", got)
	}

	// A session with no project binding falls back to the three id matches,
	// so it still sees a job it created itself.
	unbound, _ := s.ListFiltered(ctx, "", SessionScope{ID: "s1"}, "", true)
	if g := msgs(unbound); !g["weekly report"] || !g["nudge s1"] || g["other project"] {
		t.Fatalf("unbound session scope wrong: %v", g)
	}
}

// fakeSender records deliveries and can be told to fail. EnsureSession
// mirrors the pool's real contract closely enough to test the project-scoped
// modes: it actually creates the session on disk (so a reuse is observable as
// a no-op) and records the call.
type fakeSender struct {
	calls     []string
	ensured   []string
	failErr   error
	ensureErr error
	layout    agentconfig.Layout
}

func (f *fakeSender) SendWithProject(_ context.Context, sessionID, _, source, role, text, _ string) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.calls = append(f.calls, sessionID+"|"+source+"|"+role+"|"+text)
	return nil
}

func (f *fakeSender) EnsureSession(ctx context.Context, sessionID, source, projectID string) error {
	if f.ensureErr != nil {
		return f.ensureErr
	}
	f.ensured = append(f.ensured, sessionID+"|"+source+"|"+projectID)
	if _, err := session.Load(f.layout, sessionID); err == nil {
		return nil // already exists — idempotent, like the pool
	}
	_, err := session.Create(ctx, f.layout, session.CreateOptions{
		ID:     sessionID,
		Origin: session.Origin(source),
	})
	return err
}

func newRunnerLayout(t *testing.T) (agentconfig.Layout, string) {
	t.Helper()
	base := t.TempDir()
	layout := agentconfig.NewLayout(base)
	sess, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "sess-live", Origin: session.OriginUI})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return layout, sess.ID
}

func TestRunner_DeliversDue(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)

	_, _ = s.Create(context.Background(), &entity.ScheduledMessage{SessionID: sid, Message: "wake up", RunAt: time.Now().Add(-time.Second)})

	r.tick(context.Background(), zerologLogger{})

	if len(sender.calls) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(sender.calls))
	}
	want := sid + "|schedule|user|wake up"
	if sender.calls[0] != want {
		t.Fatalf("delivery = %q, want %q", sender.calls[0], want)
	}
}

func TestRunner_SendFailureMarksFailed(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout, failErr: errors.New("pool boom")}
	r := NewRunner(s, sender, layout)

	m, _ := s.Create(context.Background(), &entity.ScheduledMessage{SessionID: sid, Message: "x", RunAt: time.Now().Add(-time.Second)})
	r.tick(context.Background(), zerologLogger{})

	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusFailed || got.LastError == "" {
		t.Fatalf("failed delivery not recorded: %+v", got)
	}
}

func TestRunner_MissingSessionMarksFailed(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)

	m, _ := s.Create(context.Background(), &entity.ScheduledMessage{SessionID: "ghost", Message: "x", RunAt: time.Now().Add(-time.Second)})
	r.tick(context.Background(), zerologLogger{})

	if len(sender.calls) != 0 {
		t.Fatalf("delivered into a missing session")
	}
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusFailed {
		t.Fatalf("missing session not marked failed: %+v", got)
	}
}

// A project-scoped schedule creates the session it delivers into, so it does
// NOT need one to pre-exist — the fix for a recurring job dying because its
// target session was reaped.
func TestRunner_ProjectScopedNewMintsSession(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		Message:     "weekly report",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	wantSession := "sch-" + shortScheduleID(m.ID) + "-1"
	if len(sender.ensured) != 1 || sender.ensured[0] != wantSession+"|schedule|proj-1" {
		t.Fatalf("ensured = %v, want [%s|schedule|proj-1]", sender.ensured, wantSession)
	}
	if len(sender.calls) != 1 || sender.calls[0] != wantSession+"|schedule|user|weekly report" {
		t.Fatalf("calls = %v", sender.calls)
	}
	// The fire's target is recorded so the UI can link the row to its run.
	got, _ := s.Get(ctx, m.ID)
	if got.LastSessionID != wantSession {
		t.Fatalf("last_session_id = %q, want %q", got.LastSessionID, wantSession)
	}
	if got.Status != entity.ScheduledStatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
}

// Each fire of a recurring mode=new schedule gets its own session, so no run
// inherits the previous run's context.
func TestRunner_ProjectScopedNewIsFreshEachFire(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		Kind:        entity.ScheduledKindRecurring,
		IntervalMs:  (time.Millisecond).Milliseconds(),
		Message:     "poll",
		RunAt:       time.Now().Add(-time.Second),
	})
	// Two ticks, two fires (the 1ms interval is due again immediately).
	r.tick(ctx, zerologLogger{})
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 2 {
		t.Fatalf("want 2 deliveries, got %d: %v", len(sender.calls), sender.calls)
	}
	if sender.calls[0] == sender.calls[1] {
		t.Fatalf("second fire reused the first session: %v", sender.calls)
	}
	short := shortScheduleID(m.ID)
	for i, want := range []string{"sch-" + short + "-1", "sch-" + short + "-2"} {
		if sender.calls[i] != want+"|schedule|user|poll" {
			t.Fatalf("fire %d = %q, want session %q", i+1, sender.calls[i], want)
		}
	}
}

// Template mode is the opposite: fires that render the same id share one
// session, and EnsureSession's idempotence is what makes the reuse safe.
func TestRunner_ProjectScopedTemplateReusesSession(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:       "proj-1",
		SessionMode:     entity.ScheduledSessionTemplate,
		SessionTemplate: "nightly-build",
		Kind:            entity.ScheduledKindRecurring,
		IntervalMs:      (time.Millisecond).Milliseconds(),
		Message:         "build",
		RunAt:           time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 2 {
		t.Fatalf("want 2 deliveries, got %d", len(sender.calls))
	}
	for i, c := range sender.calls {
		if c != "nightly-build|schedule|user|build" {
			t.Fatalf("fire %d landed in %q", i+1, c)
		}
	}
}

// A schedule whose target can't be resolved (here: a mode that needs a project
// but has none) must stop rather than spin on every tick.
func TestRunner_BadTargetMarksFailed(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionMode: entity.ScheduledSessionNew,
		Message:     "x",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 0 || len(sender.ensured) != 0 {
		t.Fatalf("delivered despite an unresolvable target")
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusFailed || !strings.Contains(got.LastError, "resolve target") {
		t.Fatalf("bad target not recorded: %+v", got)
	}
}

// A failure to materialize the session is a delivery failure, not a silent
// skip: nothing is sent and the row records why.
func TestRunner_EnsureSessionFailureMarksFailed(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout, ensureErr: errors.New("disk full")}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		Message:     "x",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 0 {
		t.Fatalf("delivered without a session")
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusFailed || !strings.Contains(got.LastError, "disk full") {
		t.Fatalf("ensure failure not recorded: %+v", got)
	}
}

// The session-scoped path still resolves the project LIVE from the target
// session's meta, so a session that moved projects delivers to the right cwd.
func TestRunner_ExistingModeStampsLastSession(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: sid, Message: "x", RunAt: time.Now().Add(-time.Second)})
	r.tick(ctx, zerologLogger{})

	if len(sender.ensured) != 0 {
		t.Fatalf("session-scoped fire should not mint a session: %v", sender.ensured)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.LastSessionID != sid {
		t.Fatalf("last_session_id = %q, want %q", got.LastSessionID, sid)
	}
}
