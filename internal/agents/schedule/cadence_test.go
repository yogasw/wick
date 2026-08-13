package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// A recurring interval schedule's series is ABSOLUTE: fire N is
// anchor + N*interval. Nothing that happens to a fire — arriving late, being
// paused, being run manually — may move the series. These tests are the
// regression net for the drift bugs found in live testing: each one previously
// shifted every future fire permanently.

func TestAdvance_LateFireDoesNotDrift(t *testing.T) {
	anchor := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		Anchor: &anchor,
	}

	// The poll granularity means a fire lands a few seconds late. Advancing
	// from the fire time (the old behavior) pushed the next one to 10:00:04,
	// then 11:00:08 — a schedule that slowly walks off the hour.
	firedAt := anchor.Add(4 * time.Second)
	next, err := advance(m, firedAt, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := anchor.Add(time.Hour)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v (a late fire must not shift the series)", next, want)
	}
}

func TestAdvance_DriftFreeOverManyFires(t *testing.T) {
	anchor := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		Anchor: &anchor,
	}

	// Every fire is 5s late; after 24 of them the old code was 2 minutes off.
	for n := 1; n <= 24; n++ {
		scheduled := anchor.Add(time.Duration(n-1) * time.Hour)
		next, err := advance(m, scheduled.Add(5*time.Second), n)
		if err != nil {
			t.Fatal(err)
		}
		want := anchor.Add(time.Duration(n) * time.Hour)
		if !next.Equal(want) {
			t.Fatalf("fire %d: next = %v, want %v", n, next, want)
		}
	}
}

func TestNextFrom_ResumeLandsOnTheSeries(t *testing.T) {
	anchor := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		Anchor: &anchor,
	}

	// Paused at 10:10, resumed at 10:50. The next fire is 11:00 — the slot the
	// schedule would have hit anyway. Resuming used to mean "now + interval",
	// i.e. 11:50, which re-anchored an hourly schedule off the hour forever.
	next, err := NextFrom(m, anchor.Add(110*time.Minute)) // 10:50
	if err != nil {
		t.Fatal(err)
	}
	want := anchor.Add(2 * time.Hour) // 11:00
	if !next.Equal(want) {
		t.Fatalf("resume next = %v, want %v", next, want)
	}
}

func TestNextFrom_ResumeAfterLongPauseSkipsMissedSlots(t *testing.T) {
	anchor := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		Anchor: &anchor,
	}

	// Resumed a month later: the next fire is the next slot from NOW, not a
	// backlog of 720 missed ones (and not a million loop iterations to find).
	from := anchor.Add(30 * 24 * time.Hour).Add(30 * time.Minute)
	next, err := NextFrom(m, from)
	if err != nil {
		t.Fatal(err)
	}
	if !next.After(from) {
		t.Fatalf("next = %v, must be after %v", next, from)
	}
	if d := next.Sub(from); d > time.Hour {
		t.Fatalf("next is %v away, want within one interval", d)
	}
	// Still exactly on the series.
	if off := next.Sub(anchor) % time.Hour; off != 0 {
		t.Fatalf("next = %v is %v off the series", next, off)
	}
}

func TestNextInSeries_NoAnchorFallsBackToStepping(t *testing.T) {
	// Rows written before the anchor column existed have none; they keep the
	// old stepping behavior rather than getting a wrong series.
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
	}
	from := time.Date(2026, 8, 13, 9, 30, 0, 0, time.UTC)
	next, err := nextInSeries(m, from)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Equal(from.Add(time.Hour)) {
		t.Fatalf("legacy row: next = %v, want %v", next, from.Add(time.Hour))
	}
}

func TestNextInSeries_AnchorInTheFutureIsTheFirstFire(t *testing.T) {
	anchor := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	m := entity.ScheduledMessage{
		Kind: entity.ScheduledKindRecurring, IntervalMs: time.Hour.Milliseconds(),
		Anchor: &anchor,
	}
	next, err := nextInSeries(m, anchor.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !next.Equal(anchor) {
		t.Fatalf("next = %v, want the anchor %v", next, anchor)
	}
}

func TestCreate_SeedsAnchorForIntervalOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Now().Add(time.Hour)

	interval, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
		IntervalMs: time.Hour.Milliseconds(), RunAt: at,
	})
	if interval.Anchor == nil {
		t.Fatal("interval schedule got no anchor; its series would drift")
	}
	if !interval.Anchor.Equal(at) {
		t.Fatalf("anchor = %v, want the first fire %v", interval.Anchor, at)
	}

	// Cron needs none: the expression IS the series.
	cron, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: "s", Message: "x", Kind: entity.ScheduledKindRecurring,
		Cron: "0 9 * * 1", RunAt: at,
	})
	if cron.Anchor != nil {
		t.Fatalf("cron schedule should carry no anchor, got %v", cron.Anchor)
	}

	once, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: "s", Message: "x", RunAt: at})
	if once.Anchor != nil {
		t.Fatalf("one-shot should carry no anchor, got %v", once.Anchor)
	}
}

// A listing answers "what happens next", so live schedules come first with the
// soonest fire at the top — and the LIMIT then truncates old history instead of
// the imminent fires. This is the S1 regression: a plain `run_at DESC` put the
// furthest-future live row first and cut the next fire off the bottom.
func TestList_LiveFirstSoonestFirstThenHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	mk := func(msg, status string, runAt time.Time) {
		m, err := s.Create(ctx, &entity.ScheduledMessage{
			SessionID: "s", OwnerUserID: "u", Message: msg, RunAt: runAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if status != entity.ScheduledStatusPending {
			last := runAt
			if err := s.db.Model(&entity.ScheduledMessage{}).Where("id = ?", m.ID).
				Updates(map[string]any{"status": status, "last_run_at": last}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	// Two live (one soon, one far) and two finished (one recent, one old).
	mk("live-far", entity.ScheduledStatusPending, now.Add(48*time.Hour))
	mk("done-old", entity.ScheduledStatusDone, now.Add(-72*time.Hour))
	mk("live-soon", entity.ScheduledStatusPending, now.Add(1*time.Minute))
	mk("done-recent", entity.ScheduledStatusDone, now.Add(-1*time.Hour))

	rows, err := s.List(ctx, ListQuery{AllOwners: true})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Message)
	}
	want := []string{"live-soon", "live-far", "done-recent", "done-old"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}

	// And the cap keeps the imminent fires rather than the history.
	capped, err := s.List(ctx, ListQuery{AllOwners: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 || capped[0].Message != "live-soon" || capped[1].Message != "live-far" {
		t.Fatalf("a capped list dropped the live rows: %+v", capped)
	}
}

// run_now is a DRY run: an extra fire that leaves the schedule's definition,
// its place in the series, and its max_runs budget untouched. The note the
// create response prints says exactly that, so it has to be true.

func TestRunNow_DoesNotConsumeMaxRunsOrShiftCadence(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	// Hourly, capped at 2 runs, first fire an hour out.
	firstFire := time.Now().Add(time.Hour).Truncate(time.Second)
	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: sid, Message: "poll", Kind: entity.ScheduledKindRecurring,
		IntervalMs: time.Hour.Milliseconds(), RunAt: firstFire, MaxRuns: 2,
	})

	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 1 {
		t.Fatalf("manual run did not deliver: %v", sender.calls)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.RunCount != 0 {
		t.Fatalf("run_count = %d after a manual run, want 0 (it must not spend max_runs)", got.RunCount)
	}
	if got.ManualRuns != 1 {
		t.Fatalf("manual_runs = %d, want 1", got.ManualRuns)
	}
	if got.LastRunAt == nil {
		t.Fatal("last_run_at not stamped; the fire did happen")
	}
	if got.RunAt.Sub(firstFire).Abs() > time.Second {
		t.Fatalf("next fire moved to %v, want it left at %v", got.RunAt, firstFire)
	}
	if got.Status != entity.ScheduledStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.ManualFire || got.PendingRunAt != nil {
		t.Fatalf("manual claim not cleared: manual_fire=%v pending=%v", got.ManualFire, got.PendingRunAt)
	}
}

func TestRunNow_CappedScheduleStillGetsAllItsScheduledFires(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	// max_runs=1, due now. A manual run first must not eat the single fire.
	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: sid, Message: "poll", Kind: entity.ScheduledKindRecurring,
		IntervalMs: time.Millisecond.Milliseconds(), RunAt: time.Now().Add(-time.Second),
		MaxRuns: 1,
	})
	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx, zerologLogger{}) // the manual fire
	r.tick(ctx, zerologLogger{}) // the one scheduled fire

	if len(sender.calls) != 2 {
		t.Fatalf("want 2 deliveries (1 manual + 1 scheduled), got %d", len(sender.calls))
	}
	got, _ := s.Get(ctx, m.ID)
	if got.RunCount != 1 || got.ManualRuns != 1 {
		t.Fatalf("run_count=%d manual_runs=%d, want 1 and 1", got.RunCount, got.ManualRuns)
	}
	if got.Status != entity.ScheduledStatusDone {
		t.Fatalf("status = %q, want done (max_runs reached by the SCHEDULED fire)", got.Status)
	}
}

func TestRunNow_OneShotKeepsItsOwnFire(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	at := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	m, _ := s.Create(ctx, &entity.ScheduledMessage{SessionID: sid, Message: "later", RunAt: at})

	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx, zerologLogger{})

	got, _ := s.Get(ctx, m.ID)
	// Testing a reminder must not consume it: the real fire is still pending.
	if got.Status != entity.ScheduledStatusPending {
		t.Fatalf("status = %q, want pending (its own fire is still due)", got.Status)
	}
	if got.RunAt.Sub(at).Abs() > time.Second {
		t.Fatalf("run_at = %v, want it restored to %v", got.RunAt, at)
	}
	if got.RunCount != 0 {
		t.Fatalf("run_count = %d, want 0", got.RunCount)
	}
}

func TestRunNow_ThenTheScheduledFireStillLandsOnTheSeries(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	// Due now, hourly. Manual-fire it, then let the scheduled fire happen and
	// check the FOLLOWING slot is still on the original series.
	first := time.Now().Add(-time.Second).Truncate(time.Second)
	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		SessionID: sid, Message: "poll", Kind: entity.ScheduledKindRecurring,
		IntervalMs: time.Hour.Milliseconds(), RunAt: first,
	})
	if err := s.RunNow(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	r.tick(ctx, zerologLogger{}) // manual
	r.tick(ctx, zerologLogger{}) // scheduled

	got, _ := s.Get(ctx, m.ID)
	if got.RunCount != 1 {
		t.Fatalf("run_count = %d, want 1 scheduled fire", got.RunCount)
	}
	want := first.Add(time.Hour)
	if got.RunAt.Sub(want).Abs() > time.Second {
		t.Fatalf("next = %v, want %v (on the series, not shifted by the manual run)", got.RunAt, want)
	}
}
