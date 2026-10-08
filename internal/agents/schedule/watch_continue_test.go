package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// makeDue loads the live watches and pulls their next fire into the past, so
// the next fireDueWatches runs them again without waiting out the interval.
func makeDue(r *Runner) {
	r.refreshWatches(context.Background(), zerologLogger{})
	r.watches.mu.Lock()
	defer r.watches.mu.Unlock()
	for _, e := range r.watches.entries {
		e.next = time.Now().Add(-time.Second)
	}
}

// counterScript exits with code(n) on its n-th run (1-based), counting in a
// file under dir.
func counterScript(dir, cases string) string {
	f := filepath.Join(dir, "n")
	return `n=$(cat ` + f + ` 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + f + `; ` + cases
}

func TestRunner_EveryErrorsThenRecoversSilently(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	m := newWatchRow(t, s, sid, counterScript(t.TempDir(), `[ $n -le 3 ] && { echo boom >&2; exit 3; }; exit 1`), nil)

	for i := 0; i < 4; i++ {
		makeDue(r)
		fireDueWatches(t, r)
	}
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusActive || got.LastResult != entity.WatchResultPending || got.ConsecutiveErrors != 0 {
		t.Fatalf("after 3 errors + pending: status=%s result=%s errs=%d", got.Status, got.LastResult, got.ConsecutiveErrors)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("errors below the threshold must stay silent: %v", sender.calls)
	}
}

func TestRunner_EveryErrorStreakNotifiesOnceAndKeepsRunning(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	m := newWatchRow(t, s, sid, "echo boom >&2; exit 3", nil)

	for i := 0; i < watchErrorNotifyAfter+2; i++ {
		makeDue(r)
		fireDueWatches(t, r)
	}
	// The row is checkpointed, not written per tick: the streak lives in memory.
	got, _ := s.Get(context.Background(), m.ID)
	r.watches.mu.Lock()
	errs := r.watches.entries[m.ID].m.ConsecutiveErrors
	r.watches.mu.Unlock()
	if got.Status != entity.ScheduledStatusActive || errs != watchErrorNotifyAfter+2 {
		t.Fatalf("streak: status=%s errs=%d", got.Status, errs)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("the same error must be told once: %d notices", len(sender.calls))
	}
}

func TestRunner_RunAtWatchRunsOnceAndFinishes(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	m := newWatchRow(t, s, sid, "echo still building; exit 1", func(e *entity.ScheduledMessage) {
		e.Kind, e.IntervalMs = entity.ScheduledKindOnce, 0
	})
	fireDueWatches(t, r)
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusDone || got.RunCount != 1 || len(sender.calls) != 1 {
		t.Fatalf("run_at pending: status=%s runs=%d calls=%d", got.Status, got.RunCount, len(sender.calls))
	}
	makeDue(r)
	if fireDueWatches(t, r) != 0 || len(sender.calls) != 1 {
		t.Fatal("a run_at watch ran twice")
	}
}

func TestRunner_OnMatchContinueNotifiesOnlyNewMatches(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	data := filepath.Join(t.TempDir(), "out.json")
	write := func(v string) {
		t.Helper()
		if err := os.WriteFile(data, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	steps, err := EncodeWatch([]Step{
		{Name: "read", Kind: StepKindBash, Script: "cat " + data},
		{Name: "ok?", Kind: StepKindCheck, Rules: []Rule{{Path: "state", Op: OpEquals, Value: "OK"}},
			Extract: map[string]string{"build": "build"}},
	}, OnMatchContinue)
	if err != nil {
		t.Fatal(err)
	}
	m := newWatchRow(t, s, sid, "", func(e *entity.ScheduledMessage) { e.Steps = steps })
	tick := func(r *Runner) {
		makeDue(r)
		fireDueWatches(t, r)
	}

	write(`{"state":"OK","build":1,"took":5}`)
	tick(r)
	if len(sender.calls) != 1 {
		t.Fatalf("first match: %d notices", len(sender.calls))
	}
	tick(r) // same output
	if len(sender.calls) != 1 {
		t.Fatalf("same output notified again: %d", len(sender.calls))
	}
	write(`{"state":"OK","build":1,"took":9}`) // outside the extract
	tick(r)
	if len(sender.calls) != 1 {
		t.Fatalf("field outside extract notified: %d", len(sender.calls))
	}
	write(`{"state":"OK","build":2,"took":9}`) // extract changed
	tick(r)
	if len(sender.calls) != 2 {
		t.Fatalf("new extract not notified: %d", len(sender.calls))
	}
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusActive {
		t.Fatalf("continue watch stopped: %s", got.Status)
	}

	// A restart keeps the state file: the same match is not told again.
	r.stopWatches(zerologLogger{})
	r2 := NewRunner(s, sender, layout)
	tick(r2)
	if len(sender.calls) != 2 {
		t.Fatalf("restart re-notified: %d", len(sender.calls))
	}
}

func TestRunner_ConnectorDenialIsTerminal(t *testing.T) {
	s := newTestStore(t)
	layout, sid := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	steps, _ := EncodeSteps([]Step{{Name: "get", Kind: StepKindConnector, ToolID: "conn:bb/get_pipeline"}})

	// A lookup that fails says nothing about the user: retried, still live.
	r := NewRunner(s, sender, layout).WithConnectorExecutor(NewConnectorExecutor(
		func(context.Context, string) (*entity.User, []string, error) { return nil, nil, errors.New("db down") },
		func(context.Context, string, map[string]any, string, *entity.User, []string) (string, error) {
			return "{}", nil
		}))
	m := newWatchRow(t, s, sid, "", func(e *entity.ScheduledMessage) { e.Steps = steps })
	fireDueWatches(t, r)
	got, _ := s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusActive || len(sender.calls) != 0 {
		t.Fatalf("transient lookup error stopped the watch: status=%s calls=%d", got.Status, len(sender.calls))
	}
	r.stopWatches(zerologLogger{})

	// A user that is gone cannot come back by itself: the watch fails once.
	r2 := NewRunner(s, sender, layout).WithConnectorExecutor(NewConnectorExecutor(
		func(context.Context, string) (*entity.User, []string, error) { return nil, nil, nil },
		func(context.Context, string, map[string]any, string, *entity.User, []string) (string, error) {
			return "{}", nil
		}))
	makeDue(r2)
	fireDueWatches(t, r2)
	got, _ = s.Get(context.Background(), m.ID)
	if got.Status != entity.ScheduledStatusFailed || len(sender.calls) != 1 || !strings.Contains(got.LastError, "not approved") {
		t.Fatalf("revoked user must stop the watch: status=%s calls=%d err=%q", got.Status, len(sender.calls), got.LastError)
	}
	makeDue(r2)
	if fireDueWatches(t, r2) != 0 || len(sender.calls) != 1 {
		t.Fatal("a denied watch ran again")
	}
}
