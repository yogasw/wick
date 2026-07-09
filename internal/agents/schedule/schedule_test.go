package schedule

import (
	"context"
	"errors"
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
	if err := s.Finalize(ctx, past.ID, false, time.Time{}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	got, _ := s.Get(ctx, past.ID)
	if got.Status != entity.ScheduledStatusDone {
		t.Fatalf("one-shot finalize status = %q, want done", got.Status)
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
	if err := s.Finalize(ctx, m.ID, true, next); err != nil {
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

// fakeSender records deliveries and can be told to fail.
type fakeSender struct {
	calls   []string
	failErr error
}

func (f *fakeSender) SendWithProject(_ context.Context, sessionID, _, source, role, text, _ string) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.calls = append(f.calls, sessionID+"|"+source+"|"+role+"|"+text)
	return nil
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
	sender := &fakeSender{}
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
	sender := &fakeSender{failErr: errors.New("pool boom")}
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
	sender := &fakeSender{}
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
