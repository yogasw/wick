package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestNormalizeWakeOn(t *testing.T) {
	ok := map[string]string{
		"":                     "",
		" acme_hook:team/web ": "acme_hook:team/web",
		"acme_hook:team/*":     "acme_hook:team/*",
		"acme-hook:build.done": "acme-hook:build.done",
	}
	for in, want := range ok {
		got, err := NormalizeWakeOn(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"no-colon", "Acme:x", "acme:", "acme:a b", "acme:*x", ":topic"} {
		if _, err := NormalizeWakeOn(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestWakeMatches(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"acme:team/web", "acme:team/web", true},
		{"acme:Team/Web", "acme:team/web", true},
		{"acme:team/*", "acme:team/api", true},
		{"acme:team/*", "acme:other/api", false},
		{"acme:team/web", "other:team/web", false},
	}
	for _, c := range cases {
		if got := WakeMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("%s vs %s: got %v", c.pattern, c.topic, got)
		}
	}
}

func TestAdvanceWakeOnly(t *testing.T) {
	deadline := time.Now().Add(time.Hour)
	m := entity.ScheduledMessage{Kind: entity.ScheduledKindRecurring, WakeOn: "acme:x", EndsAt: &deadline}
	if next, err := advance(m, time.Now(), 1); err != nil || !next.Equal(deadline) {
		t.Fatalf("before the limit: next=%v err=%v, want the limit", next, err)
	}
	if next, _ := advance(m, deadline.Add(time.Second), 2); !next.IsZero() {
		t.Fatalf("after the limit: next=%v, want none", next)
	}
	m.EndsAt = nil
	if next, _ := advance(m, time.Now(), 1); !next.Equal(wakeNever) {
		t.Fatalf("no limit: next=%v, want never", next)
	}
}

func newWakeWatch(t *testing.T, s *Store, sid string, endsAt time.Time) *entity.ScheduledMessage {
	t.Helper()
	steps, _ := EncodeSteps([]Step{{Name: "merged?", Kind: "check", Rules: []Rule{{Path: "action", Op: "equals", Value: "merge"}}}})
	m, err := s.Create(context.Background(), &entity.ScheduledMessage{
		SessionID: sid, OwnerUserID: "u1", Message: "MR merged", Type: entity.ScheduledTypeWatch, Steps: steps,
		Kind: entity.ScheduledKindRecurring, WakeOn: "acme_hook:team/web", EndsAt: &endsAt, RunAt: endsAt,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return m
}

func TestRunner_WakeOnlyWatchRunsOnEvent(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	deadline := time.Now().Add(time.Hour)
	m := newWakeWatch(t, s, sid, deadline)
	ctx := context.Background()

	if fireDueWatches(t, r) != 0 {
		t.Fatal("a wake-only watch must not run on its own before the limit")
	}
	if n := r.wakeWatches("other_hook:team/web", []byte(`{"action":"merge"}`)); n != 0 {
		t.Fatalf("another plugin's topic woke %d watches", n)
	}

	// A non-matching event runs the steps, stays pending, goes back to waiting.
	if n := r.wakeWatches("acme_hook:team/web", []byte(`{"action":"open"}`)); n != 1 {
		t.Fatalf("woke %d, want 1", n)
	}
	due := r.dueWatches(time.Now())
	if len(due) != 1 {
		t.Fatalf("%d due after the event, want 1", len(due))
	}
	r.runWatch(ctx, zerologLogger{}, due[0])
	r.watches.mu.Lock()
	e := r.watches.entries[m.ID]
	next, runs := e.next, e.m.RunCount
	r.watches.mu.Unlock()
	if len(sender.calls) != 0 || !next.Equal(deadline) || runs != 1 {
		t.Fatalf("after a non-matching event: calls=%d next=%v runs=%d", len(sender.calls), next, runs)
	}

	// The matching event ends it with one notice.
	r.wakeWatches("acme_hook:team/web", []byte(`{"action":"merge"}`))
	for _, e := range r.dueWatches(time.Now()) {
		r.runWatch(ctx, zerologLogger{}, e)
	}
	if len(sender.calls) != 1 || !strings.Contains(sender.calls[0], "MR merged") {
		t.Fatalf("want one notice, got %v", sender.calls)
	}
	if got, _ := s.Get(ctx, m.ID); got.Status != entity.ScheduledStatusDone {
		t.Fatalf("status = %s, want done", got.Status)
	}
}

func TestRunner_WakeOnlyWatchTimesOut(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	m := newWakeWatch(t, s, sid, time.Now().Add(-time.Second))

	if fireDueWatches(t, r) != 1 {
		t.Fatal("the time limit should make the watch due")
	}
	if len(sender.calls) != 1 || !strings.Contains(sender.calls[0], "timed out") {
		t.Fatalf("want one timeout notice, got %v", sender.calls)
	}
	if got, _ := s.Get(context.Background(), m.ID); got.Status != entity.ScheduledStatusDone {
		t.Fatalf("status = %s, want done", got.Status)
	}
}

func TestRunner_EventsQueueWhileRunning(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	m := newWakeWatch(t, s, sid, time.Now().Add(time.Hour))
	r.refreshWatches(context.Background(), zerologLogger{})

	r.wakeWatches("acme_hook:team/web", []byte(`{"action":"open"}`))
	due := r.dueWatches(time.Now()) // marks it running
	r.wakeWatches("acme_hook:team/web", []byte(`{"action":"merge"}`))
	r.runWatch(context.Background(), zerologLogger{}, due[0])

	r.watches.mu.Lock()
	e := r.watches.entries[m.ID]
	queued, next := len(e.wakeQ), e.next
	r.watches.mu.Unlock()
	if queued != 1 || next.After(time.Now()) {
		t.Fatalf("the event that arrived mid-run must run next: queued=%d next=%v", queued, next)
	}
	for _, e := range r.dueWatches(time.Now()) {
		r.runWatch(context.Background(), zerologLogger{}, e)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("want the queued merge to notify once, got %v", sender.calls)
	}
}
