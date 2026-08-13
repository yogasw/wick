package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// What the API REPORTS has to match what the schedule will actually do. These
// cover the reporting bugs found in the third live-test pass: a manual run that
// looked like it had moved the schedule, a paused row that reported "active",
// and a listing that ranked cancelled-never-fired rows above real history.

// A manual run borrows run_at to become due. Reporting that borrowed value said
// "next run: now", which reads as though run_now had moved the schedule — the
// opposite of what it does.
func TestNextRunAt_ManualFireReportsTheRealNextFire(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	realNext := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "poll", Kind: entity.ScheduledKindRecurring,
		IntervalMs: time.Hour.Milliseconds(), RunAt: realNext,
	})

	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(ctx, m.ID)
	// run_at IS now (that is how the claim finds it) …
	if got.RunAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("run_at = %v, expected it moved to now for the claim", got.RunAt)
	}
	// … but the REPORTED next fire is the schedule's own.
	next := got.NextRunAt()
	if next == nil {
		t.Fatal("no next fire reported while a manual run is pending")
	}
	if next.UTC().Truncate(time.Second) != realNext {
		t.Fatalf("reported next = %v, want the schedule's own %v", next.UTC(), realNext)
	}
	if !got.ManualFirePending() {
		t.Fatal("manual fire not reported as pending")
	}
}

func TestEffectiveStatus_PausedIsReadable(t *testing.T) {
	tests := []struct {
		name   string
		row    entity.ScheduledMessage
		want   string
		paused bool
	}{
		{
			name: "paused recurring reports paused, not active",
			row:  entity.ScheduledMessage{Status: entity.ScheduledStatusActive, Paused: true},
			want: entity.ScheduledStatusPaused,
		},
		{
			name: "unpaused active is active",
			row:  entity.ScheduledMessage{Status: entity.ScheduledStatusActive},
			want: entity.ScheduledStatusActive,
		},
		{
			name: "pending stays pending",
			row:  entity.ScheduledMessage{Status: entity.ScheduledStatusPending},
			want: entity.ScheduledStatusPending,
		},
		{
			// A terminal row's stored status wins: "cancelled while paused" is
			// cancelled, not paused.
			name: "cancelled while paused reports cancelled",
			row:  entity.ScheduledMessage{Status: entity.ScheduledStatusCancelled, Paused: true},
			want: entity.ScheduledStatusCancelled,
		},
		{
			name: "done reports done",
			row:  entity.ScheduledMessage{Status: entity.ScheduledStatusDone},
			want: entity.ScheduledStatusDone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.EffectiveStatus(); got != tc.want {
				t.Fatalf("EffectiveStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// Paused is stored as a flag beside status, so filtering for it has to
// translate: "paused" means live AND paused.
func TestList_PausedFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	mk := func(msg string, paused bool) {
		m, err := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", OwnerUserID: "u", Message: msg, RunAt: future,
			Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if paused {
			if err := s.SetPaused(ctx, m.ID, true, time.Time{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("running", false)
	mk("suspended", true)

	yes := true
	only, err := s.List(ctx, ListQuery{AllOwners: true, Statuses: LiveStatuses(), Paused: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Message != "suspended" {
		t.Fatalf("paused filter returned %+v", only)
	}
	if only[0].EffectiveStatus() != entity.ScheduledStatusPaused {
		t.Fatalf("status = %q, want paused", only[0].EffectiveStatus())
	}

	no := false
	unpaused, _ := s.List(ctx, ListQuery{AllOwners: true, Statuses: LiveStatuses(), Paused: &no})
	if len(unpaused) != 1 || unpaused[0].Message != "running" {
		t.Fatalf("unpaused filter returned %+v", unpaused)
	}

	// Without the filter, both are live and both come back.
	both, _ := s.List(ctx, ListQuery{AllOwners: true, Statuses: LiveStatuses()})
	if len(both) != 2 {
		t.Fatalf("live list dropped a paused row: %d", len(both))
	}
}

// Cancel keeps run_at when there is no last_run_at to fall back to, so a row
// cancelled before its first fire carries a FUTURE timestamp. That must not
// rank it above schedules that actually ran.
func TestList_CancelledNeverFiredSortsLast(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	// Cancelled before ever firing, with a fire time far in the future.
	never, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", OwnerUserID: "u", Message: "cancelled-never-ran",
		RunAt: now.Add(48 * time.Hour),
	})
	if err := s.Cancel(ctx, never.ID); err != nil {
		t.Fatal(err)
	}

	// Actually fired, then finished.
	fired, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", OwnerUserID: "u", Message: "ran-then-done",
		RunAt: now.Add(-time.Minute),
	})
	if _, err := s.ClaimDue(ctx, now, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(ctx, fired.ID, entity.ScheduledKindOnce, time.Time{}, "s"); err != nil {
		t.Fatal(err)
	}

	rows, err := s.List(ctx, ListQuery{AllOwners: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Message != "ran-then-done" {
		t.Fatalf("order = [%s, %s]; a cancelled-never-fired row outranked real history",
			rows[0].Message, rows[1].Message)
	}

	// And with limit 1, the row that actually ran is the one kept.
	capped, _ := s.List(ctx, ListQuery{AllOwners: true, Limit: 1})
	if len(capped) != 1 || capped[0].Message != "ran-then-done" {
		t.Fatalf("limit kept the wrong row: %+v", capped)
	}
}

// A live schedule always outranks any terminal row, however far out its fire is.
func TestList_LiveOutranksTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	done, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", OwnerUserID: "u", Message: "done", RunAt: now.Add(-time.Minute),
	})
	_, _ = s.ClaimDue(ctx, now, 10)
	_ = s.Finalize(ctx, done.ID, entity.ScheduledKindOnce, time.Time{}, "s")

	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", OwnerUserID: "u", Message: "live-far", RunAt: now.Add(30 * 24 * time.Hour),
	})

	rows, _ := s.List(ctx, ListQuery{AllOwners: true})
	if len(rows) != 2 || rows[0].Message != "live-far" {
		t.Fatalf("live row did not come first: %+v", rows)
	}
}

// The broad session filter answers "related to this session"; the strict one
// answers "what will land HERE". Both are needed, and they must differ.
func TestList_TargetSessionIDIsStrict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	// Targets s1.
	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s1", OwnerUserID: "u", Message: "into-s1", RunAt: future,
	})
	// A project job merely CREATED from s1 — it delivers elsewhere.
	_, _ = s.Create(ctx, &entity.ScheduledMessage{
		ProjectID: "p1", SessionMode: entity.ScheduledSessionNew, SourceSessionID: "s1",
		OwnerUserID: "u", Message: "job-from-s1", RunAt: future,
	})

	broad, _ := s.List(ctx, ListQuery{AllOwners: true, Scope: SessionScope{ID: "s1"}})
	if len(broad) != 2 {
		t.Fatalf("broad session filter should show both: %+v", broad)
	}

	strict, _ := s.List(ctx, ListQuery{AllOwners: true, TargetSessionID: "s1"})
	if len(strict) != 1 || strict[0].Message != "into-s1" {
		t.Fatalf("strict filter returned %+v, want only the row targeting s1", strict)
	}
}

// A manual fire in mode=new must not be named like scheduled fire #0 — the
// scheduled series is 1-based, so "-0" both broke the convention and invited
// confusion with the first real run.
func TestResolveTarget_ManualFireHasItsOwnSessionName(t *testing.T) {
	firedAt := time.Now()
	row := entity.ScheduledMessage{
		ID: "sm_abcd1234", ProjectID: "p1", SessionMode: entity.ScheduledSessionNew,
	}

	// Scheduled fire: numbered by run count.
	row.RunCount = 1
	scheduled, _, err := ResolveTarget(row, firedAt)
	if err != nil {
		t.Fatal(err)
	}
	if scheduled != "sch-abcd1234-1" {
		t.Fatalf("scheduled fire session = %q", scheduled)
	}

	// Manual fire: distinct namespace, never "-0".
	row.RunCount = 0
	row.ManualFire = true
	row.ManualRuns = 1
	manual, _, err := ResolveTarget(row, firedAt)
	if err != nil {
		t.Fatal(err)
	}
	if manual == "sch-abcd1234-0" {
		t.Fatal("manual fire still uses the off-by-one scheduled numbering")
	}
	if manual != "sch-abcd1234-manual-1" {
		t.Fatalf("manual fire session = %q, want sch-abcd1234-manual-1", manual)
	}
	if manual == scheduled {
		t.Fatal("manual and scheduled fires collided on the same session")
	}
}
